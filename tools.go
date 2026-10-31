package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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
		s.listStrips)
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

// addTool runs f under the server's lock. The SDK sends what f returns as JSON (text and structuredContent)
// and an error as a tool error.
func addTool[In any](srv *mcp.Server, s *Server, name string, readOnly bool, desc string, f func(In) (any, error)) {
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: desc, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly}},
		func(_ context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			out, err := f(in)
			if err != nil {
				slog.Warn("tool refused", "tool", name, "err", err)
			}
			return nil, out, err
		})
}

// header names the strip in every answer: its id, the name set on the desk (its default name when empty;
// null when the desk did not answer), and linked_with when the desk says its stereo pair is linked.
func header(got Reply, st strip) map[string]any {
	out := map[string]any{"id": st.id(), "name": nil}
	if name, ok := got.first(st.nameAddr()).(string); ok {
		out["name"] = cmp.Or(name, st.defaultName())
	}
	if partner, link, ok := st.pair(); ok && got.first(link) == int32(1) {
		out["linked_with"] = partner.id()
	}
	return out
}

// readAll asks the desk for addrsOf every strip; errSilent when nothing answers.
func (s *Server) readAll(addrsOf func(strip) []string) (Reply, error) {
	var addrs []string
	for _, st := range allStrips {
		addrs = append(addrs, addrsOf(st)...)
	}
	got := s.desk.Get(addrs...)
	if len(got) == 0 {
		return nil, errSilent
	}
	return got, nil
}

func (s *Server) resolve(ref string) (strip, error) {
	if strings.TrimSpace(ref) == "" {
		return strip{}, errors.New("strip is required")
	}
	if st, ok := idOf(ref); ok {
		return st, nil
	}
	got, err := s.readAll(func(st strip) []string { return []string{st.nameAddr()} })
	if err != nil {
		return strip{}, err
	}
	names := map[strip]string{}
	for _, st := range allStrips {
		if name, ok := got.first(st.nameAddr()).(string); ok {
			names[st] = name
		}
	}
	return pick(ref, names)
}

func (s *Server) listStrips(struct{}) (any, error) {
	got, err := s.readAll(strip.headerAddrs)
	if err != nil {
		return nil, err
	}
	var list []map[string]any
	for _, st := range allStrips {
		list = append(list, header(got, st))
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
	tree := map[string]any{"fader_db": st.fader(), "muted": st.mute()}
	if st.k.pan {
		tree["pan"] = st.pan()
	}
	if st.k.bands > 0 {
		var bands []any
		for b := 1; b <= st.k.bands; b++ {
			band := map[string]any{"band": b}
			for _, f := range bandFields {
				band[f.name] = st.eq(b, f)
			}
			bands = append(bands, band)
		}
		tree["eq"] = map[string]any{"on": st.eqOn(), "bands": bands}
	}
	if st.k.comp {
		comp := map[string]any{}
		for _, f := range dynFields {
			comp[f.name] = st.dyn(f)
		}
		tree["comp"] = comp
	}
	addrs := st.headerAddrs()
	if st.k.input > 0 {
		addrs = append(addrs, st.haIndexAddr())
	}
	collect(tree, &addrs)
	got := s.desk.Get(addrs...)
	if len(got) == 0 {
		return nil, errSilent
	}
	out := header(got, st)
	maps.Copy(out, fill(tree, got).(map[string]any))
	if st.k.input > 0 {
		out["preamp"] = s.preamp(got, st)
	}
	return out, nil
}

// preamp reads the headamp that feeds the strip (doc p.43). null: the desk did not say which one.
// An internal source (index -1) has no preamp.
func (s *Server) preamp(got Reply, st strip) any {
	idx, ok := got.first(st.haIndexAddr()).(int32)
	if !ok {
		return nil
	}
	if idx < 0 {
		return map[string]any{"source": "internal"}
	}
	tree := map[string]any{"headamp": idx, "gain_db": headampGain(int(idx)), "phantom_48v": headampPhantom(int(idx))}
	var addrs []string
	collect(tree, &addrs)
	return fill(tree, s.desk.Get(addrs...))
}

// write is a value a tool was asked to set; apply checks it.
type write struct {
	p   param
	val any
}

// opt adds a write when the tool was given that optional argument.
func opt[T any](ws []write, v *T, p param) []write {
	if v == nil {
		return ws
	}
	return append(ws, write{p, *v})
}

// apply is the safety rule: refuse a value the desk cannot hold, read the current values and refuse if the
// desk does not answer, so nothing is sent; then send, read back and report {from, to}.
func (s *Server) apply(st strip, ws []write) (any, error) {
	if len(ws) == 0 {
		return nil, errors.New("give at least one value to change")
	}
	args := make([]any, len(ws))
	var addrs []string
	for i, w := range ws {
		arg, err := w.p.encode(w.val)
		if err != nil {
			return nil, fmt.Errorf("%s %v %w; nothing was sent", w.p.name, w.val, err)
		}
		args[i], addrs = arg, append(addrs, w.p.addr)
	}
	before := s.desk.Get(append(st.headerAddrs(), addrs...)...)
	for _, w := range ws {
		if _, ok := before.value(w.p); !ok {
			return nil, fmt.Errorf("%w: no reply for %s %s; nothing was sent", errSilent, st.id(), w.p.name)
		}
	}
	for i, w := range ws {
		slog.Info("set", "strip", st.id(), "param", w.p.name, "from", before.valueOrNil(w.p), "to", w.val)
		if err := s.desk.Set(w.p.addr, args[i]); err != nil {
			return nil, err
		}
	}
	return report(st, ws, before, s.desk.Get(addrs...)), nil
}

func report(st strip, ws []write, before, after Reply) map[string]any {
	changed := map[string]any{}
	for _, w := range ws {
		changed[w.p.name] = map[string]any{"from": before.valueOrNil(w.p), "to": after.valueOrNil(w.p)}
	}
	out := header(before, st)
	out["changed"] = changed
	if partner, ok := out["linked_with"]; ok {
		out["note"] = fmt.Sprintf("this strip is linked: the desk may apply the change to %s as well", partner)
	}
	return out
}

// change resolves the strip, lets `build` turn the arguments into writes, and applies them.
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
	return s.change(in.Strip, func(st strip) ([]write, error) { return []write{{st.fader(), in.DB}}, nil })
}

func (s *Server) setMute(in muteArgs) (any, error) {
	return s.change(in.Strip, func(st strip) ([]write, error) { return []write{{st.mute(), in.Muted}}, nil })
}

func (s *Server) setEQBand(in eqArgs) (any, error) {
	return s.change(in.Strip, func(st strip) ([]write, error) {
		if n := st.k.bands; in.Band < 1 || in.Band > n {
			return nil, fmt.Errorf("%s has %d EQ bands; band %d does not exist", st.id(), n, in.Band)
		}
		ws := opt(nil, in.GainDB, st.eq(in.Band, bandGain))
		ws = opt(ws, in.FreqHz, st.eq(in.Band, bandFreq))
		return opt(ws, in.Q, st.eq(in.Band, bandQ)), nil
	})
}

func (s *Server) setComp(in compArgs) (any, error) {
	return s.change(in.Strip, func(st strip) ([]write, error) {
		if !st.k.comp {
			return nil, fmt.Errorf("%s has no comp (only inputs, buses, matrices and mains do)", st.id())
		}
		ws := opt(nil, in.Threshold, st.dyn(dynThr))
		ws = opt(ws, in.Ratio, st.dyn(dynRatio))
		ws = opt(ws, in.MakeupDB, st.dyn(dynMakeup))
		ws = opt(ws, in.AttackMs, st.dyn(dynAttack))
		ws = opt(ws, in.ReleaseMs, st.dyn(dynRelease))
		return opt(ws, in.On, st.dyn(dynOn)), nil // on last, so a new threshold is in place when the comp switches on
	})
}
