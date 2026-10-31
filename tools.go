package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server exposes one desk as MCP tools. Tool calls run one at a time: a write is read, send, read back.
type Server struct {
	desk *Desk
	mu   sync.Mutex
}

const stripDoc = "Strip id (ch/01..32, auxin/01..08, fxrtn/01..08, bus/01..16, mtx/01..06, main/st, main/m, dca/1..8) " +
	"or its desk name (case and accents ignored; an ambiguous name is refused with the candidates)."

type stripArg struct {
	Strip string `json:"strip" jsonschema:"strip id or desk name"`
}

type faderArgs struct {
	Strip string  `json:"strip"`
	DB    float64 `json:"db" jsonschema:"fader level in dB, -90 (fully down) to +10; 0 is unity"`
}

type muteArgs struct {
	Strip string `json:"strip"`
	Muted bool   `json:"muted" jsonschema:"true mutes the strip, false un-mutes it"`
}

type eqArgs struct {
	Strip  string   `json:"strip"`
	Band   int      `json:"band" jsonschema:"EQ band, 1-4 on inputs, aux ins and FX returns; 1-6 on buses, matrices and mains"`
	GainDB *float64 `json:"gain_db,omitempty" jsonschema:"band gain in dB, -15 to +15"`
	FreqHz *float64 `json:"freq_hz,omitempty" jsonschema:"band frequency in Hz, 20 to 20000"`
	Q      *float64 `json:"q,omitempty" jsonschema:"band Q, 0.3 (wide) to 10 (narrow)"`
}

type compArgs struct {
	Strip     string   `json:"strip"`
	Threshold *float64 `json:"threshold_db,omitempty" jsonschema:"threshold in dB, -60 to 0"`
	Ratio     *float64 `json:"ratio,omitempty" jsonschema:"one of 1.1 1.3 1.5 2 2.5 3 4 5 7 10 20 100 (higher is harder)"`
	MakeupDB  *float64 `json:"makeup_db,omitempty" jsonschema:"makeup gain in dB, 0 to 24"`
	AttackMs  *float64 `json:"attack_ms,omitempty" jsonschema:"attack in ms, 0 to 120"`
	ReleaseMs *float64 `json:"release_ms,omitempty" jsonschema:"release in ms, 5 to 4000"`
	On        *bool    `json:"on,omitempty" jsonschema:"switch the comp on or off"`
}

func newMCP(desk *Desk) *mcp.Server {
	s := &Server{desk: desk}
	srv := mcp.NewServer(&mcp.Implementation{Name: "thumbmix-mcp", Version: "0.1.0"}, nil)
	const writes = " Reads the current value first and refuses if the desk does not answer; reports {from, to} read back. " +
		"Values snap to the desk's own steps, so `to` can differ slightly from what was asked. "
	addTool(srv, s, "list_strips", true, "List every strip (inputs, aux ins, FX returns, buses, matrices, mains, DCAs) "+
		"with its id, the name set on the desk, and linked_with when it is one side of a stereo pair. Use it to find strips by name.",
		func(struct{}) (any, error) { return s.listStrips() })
	addTool(srv, s, "get_strip", true, "Read one strip: fader_db (-90 = fully down), muted, pan (-100 left to 100 right), "+
		"eq (on, bands with type, freq_hz, gain_db, q), comp (on, mode, threshold_db, ratio, attack_ms, release_ms, makeup_db) "+
		"and, where a preamp feeds it, preamp (gain_db, phantom_48v; read only). null means the desk did not answer. "+stripDoc, s.getStrip)
	addTool(srv, s, "set_fader", false, "Set a strip's fader level in dB."+writes+stripDoc, s.setFader)
	addTool(srv, s, "set_mute", false, "Mute or un-mute a strip."+writes+stripDoc, s.setMute)
	addTool(srv, s, "set_eq_band", false, "Change an EQ band's gain, frequency or Q; give at least one. "+
		"Read get_strip first and change by a few dB at a time."+writes+stripDoc, s.setEQBand)
	addTool(srv, s, "set_comp", false, "Change a strip's comp; give at least one. Only inputs, buses, matrices and mains have one. "+
		"If get_strip shows mode EXP these parameters belong to the expander."+writes+stripDoc, s.setComp)
	return srv
}

func addTool[In any](srv *mcp.Server, s *Server, name string, readOnly bool, desc string, f func(In) (any, error)) {
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: desc, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly}},
		func(_ context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			out, err := f(in)
			if err != nil {
				slog.Warn("tool refused", "tool", name, "err", err)
				return nil, nil, err
			}
			text, err := json.MarshalIndent(out, "", " ")
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}}, nil, nil
		})
}

// rawName is the name set on the desk, which may be empty; false when the desk did not answer.
func rawName(got Reply, st strip) (string, bool) {
	if args := got[st.nameAddr()]; len(args) > 0 {
		name, ok := args[0].(string)
		return name, ok
	}
	return "", false
}

func displayName(got Reply, st strip) any {
	name, ok := rawName(got, st)
	switch {
	case !ok:
		return nil
	case name == "":
		return st.defaultName()
	}
	return name
}

// linkedWith is the pair partner's id when the desk says the pair is linked.
func linkedWith(got Reply, st strip) (string, bool) {
	partner, ok := st.partner()
	if args := got[st.linkAddr()]; ok && len(args) > 0 && args[0] == int32(1) {
		return partner.id(), true
	}
	return "", false
}

func (s *Server) allNames() (map[strip]string, Reply, error) {
	var addrs []string
	for _, st := range allStrips() {
		addrs = append(addrs, st.nameAddr(), st.linkAddr())
	}
	got := s.desk.Get(addrs...)
	if len(got) == 0 {
		return nil, nil, errSilent
	}
	names := map[strip]string{}
	for _, st := range allStrips() {
		if name, ok := rawName(got, st); ok {
			names[st] = name
		}
	}
	return names, got, nil
}

func (s *Server) resolve(ref string) (strip, error) {
	if strings.TrimSpace(ref) == "" {
		return strip{}, errors.New("strip is required")
	}
	if st, ok := idOf(ref); ok {
		return st, nil
	}
	names, _, err := s.allNames()
	if err != nil {
		return strip{}, err
	}
	return pick(ref, names)
}

func (s *Server) listStrips() (any, error) {
	_, got, err := s.allNames()
	if err != nil {
		return nil, err
	}
	var list []map[string]any
	for _, st := range allStrips() {
		e := map[string]any{"id": st.id(), "name": displayName(got, st)}
		if partner, ok := linkedWith(got, st); ok {
			e["linked_with"] = partner
		}
		list = append(list, e)
	}
	return map[string]any{"strips": list}, nil
}

// collect and fill render an output tree whose leaves are params: collect lists the addresses to read,
// fill swaps each leaf for what the desk said.
func collect(v any, addrs *[]string) {
	switch v := v.(type) {
	case param:
		*addrs = append(*addrs, v.addr)
	case map[string]any:
		for _, x := range v {
			collect(x, addrs)
		}
	case []any:
		for _, x := range v {
			collect(x, addrs)
		}
	}
}

// What the desk did not answer becomes null, never a guess.
func fill(v any, got Reply) any {
	switch v := v.(type) {
	case param:
		return got.valueOrNil(v)
	case map[string]any:
		m := map[string]any{}
		for k, x := range v {
			m[k] = fill(x, got)
		}
		return m
	case []any:
		l := make([]any, len(v))
		for i, x := range v {
			l[i] = fill(x, got)
		}
		return l
	}
	return v
}

func (s *Server) getStrip(in stripArg) (any, error) {
	st, err := s.resolve(in.Strip)
	if err != nil {
		return nil, err
	}
	k := st.k()
	tree := map[string]any{"fader_db": st.fader(), "muted": st.mute()}
	if k.pan {
		tree["pan"] = st.pan()
	}
	if k.bands > 0 {
		var bands []any
		for b := 1; b <= k.bands; b++ {
			band := map[string]any{"band": b}
			for f := range eqFields {
				band[st.eq(b, f).name] = st.eq(b, f)
			}
			bands = append(bands, band)
		}
		tree["eq"] = map[string]any{"on": st.eqOn(), "bands": bands}
	}
	if k.dyn {
		comp := map[string]any{}
		for f := range dynFields {
			comp[st.dyn(f).name] = st.dyn(f)
		}
		tree["comp"] = comp
	}
	addrs := []string{st.nameAddr(), st.linkAddr()}
	if k.preamp {
		addrs = append(addrs, st.haIndexAddr())
	}
	collect(tree, &addrs)
	got := s.desk.Get(addrs...)
	if len(got) == 0 {
		return nil, errSilent
	}
	out := fill(tree, got).(map[string]any)
	out["id"], out["name"] = st.id(), displayName(got, st)
	if partner, ok := linkedWith(got, st); ok {
		out["linked_with"] = partner
	}
	if k.preamp {
		out["preamp"] = s.preamp(got, st)
	}
	return out, nil
}

// preamp reads the headamp that feeds the strip (doc p.43). null: the desk did not say which one.
// An internal source (index -1) has no preamp.
func (s *Server) preamp(got Reply, st strip) any {
	args := got[st.haIndexAddr()]
	if len(args) == 0 {
		return nil
	}
	idx, _ := args[0].(int32)
	if idx < 0 {
		return map[string]any{"source": "internal"}
	}
	tree := map[string]any{"headamp": idx, "gain_db": headampGain(int(idx)), "phantom_48v": headampPhantom(int(idx))}
	var addrs []string
	collect(tree, &addrs)
	return fill(tree, s.desk.Get(addrs...))
}

type write struct {
	p   param
	val any
}

// field is an optional number a tool was given for a param.
type field struct {
	v *float64
	p param
}

// numeric turns the given fields into writes after checking each against what the desk can hold.
func numeric(fs ...field) ([]write, error) {
	var ws []write
	for _, f := range fs {
		if f.v == nil {
			continue
		}
		if err := f.p.check(*f.v); err != nil {
			return nil, err
		}
		ws = append(ws, write{f.p, *f.v})
	}
	return ws, nil
}

// apply is the safety rule: read the current values, refuse and send nothing if the desk does not answer,
// then send, then read back and report {from, to}.
func (s *Server) apply(st strip, ws []write) (any, error) {
	if len(ws) == 0 {
		return nil, errors.New("give at least one value to change")
	}
	addrs := []string{st.nameAddr(), st.linkAddr()}
	for _, w := range ws {
		addrs = append(addrs, w.p.addr)
	}
	before := s.desk.Get(addrs...)
	from := map[string]any{}
	for _, w := range ws {
		v, ok := before.value(w.p)
		if !ok {
			return nil, fmt.Errorf("%w: no reply for %s %s; nothing was sent", errSilent, st.id(), w.p.name)
		}
		from[w.p.name] = v
	}
	for _, w := range ws {
		slog.Info("set", "strip", st.id(), "param", w.p.name, "from", from[w.p.name], "to", w.val)
		if err := s.desk.Set(w.p.addr, w.p.encode(w.val)); err != nil {
			return nil, err
		}
	}
	after := s.desk.Get(addrs...)
	changed := map[string]any{}
	for _, w := range ws {
		changed[w.p.name] = map[string]any{"from": from[w.p.name], "to": after.valueOrNil(w.p)}
	}
	out := map[string]any{"strip": st.id(), "name": displayName(before, st), "changed": changed}
	if partner, ok := linkedWith(before, st); ok {
		out["linked_with"] = partner
		out["note"] = "this strip is linked: the desk may apply the change to " + partner + " as well"
	}
	return out, nil
}

// change resolves the strip, lets `build` validate the arguments into writes, and applies them.
func (s *Server) change(ref string, build func(strip) ([]write, error)) (any, error) {
	st, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	ws, err := build(st)
	if err != nil {
		return nil, err
	}
	return s.apply(st, ws)
}

func (s *Server) setFader(in faderArgs) (any, error) {
	return s.change(in.Strip, func(st strip) ([]write, error) { return numeric(field{&in.DB, st.fader()}) })
}

func (s *Server) setMute(in muteArgs) (any, error) {
	return s.change(in.Strip, func(st strip) ([]write, error) { return []write{{st.mute(), in.Muted}}, nil })
}

func (s *Server) setEQBand(in eqArgs) (any, error) {
	return s.change(in.Strip, func(st strip) ([]write, error) {
		if n := st.k().bands; in.Band < 1 || in.Band > n {
			return nil, fmt.Errorf("%s has %d EQ bands; band %d does not exist", st.id(), n, in.Band)
		}
		return numeric(field{in.GainDB, st.eq(in.Band, "g")}, field{in.FreqHz, st.eq(in.Band, "f")}, field{in.Q, st.eq(in.Band, "q")})
	})
}

func (s *Server) setComp(in compArgs) (any, error) {
	return s.change(in.Strip, func(st strip) ([]write, error) {
		if !st.k().dyn {
			return nil, fmt.Errorf("%s has no comp (only inputs, buses, matrices and mains do)", st.id())
		}
		ws, err := numeric(field{in.Threshold, st.dyn("thr")}, field{in.Ratio, st.dyn("ratio")}, field{in.MakeupDB, st.dyn("mgain")},
			field{in.AttackMs, st.dyn("attack")}, field{in.ReleaseMs, st.dyn("release")})
		if in.On != nil { // last, so a new threshold is in place when the comp switches on
			ws = append(ws, write{st.dyn("on"), *in.On})
		}
		return ws, err
	})
}
