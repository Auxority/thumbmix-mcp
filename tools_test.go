package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func f64(v float64) *float64 { return &v }

func changed(t *testing.T, res any, key string) map[string]any {
	t.Helper()
	m := res.(map[string]any)["changed"].(map[string]any)[key]
	if m == nil {
		t.Fatalf("no change reported for %s in %v", key, res)
	}
	return m.(map[string]any)
}

func TestSetEQBandChangesTheDeskAndReportsFromTo(t *testing.T) {
	f, s := bassDesk(t)
	res, err := s.setEQBand(eqArgs{Strip: "ch/01", Band: 1, GainDB: f64(3), Q: f64(1)})
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := f.value(mustStrip("ch/01").eq(1, bandGain)); g != 3.0 {
		t.Errorf("desk gain = %v, want 3", g)
	}
	if got := changed(t, res, "gain_db"); got["from"] != 0.0 || got["to"] != 3.0 {
		t.Errorf("gain_db = %v", got)
	}
	if got := changed(t, res, "q"); got["from"] != 1.96 || got["to"] != 0.982 { // the desk snaps to its own grid
		t.Errorf("q = %v", got)
	}
	if f.setCount() != 2 {
		t.Errorf("%d sets sent, want 2 (freq was not asked for)", f.setCount())
	}
}

func TestSilentDeskRefusesAndSendsNothing(t *testing.T) {
	f, s := bassDesk(t)
	f.setSilent(true)
	_, err := s.setEQBand(eqArgs{Strip: "ch/01", Band: 1, GainDB: f64(3)})
	if !errors.Is(err, errSilent) || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("err = %v", err)
	}
	f.setSilent(false)
	if f.setCount() != 0 {
		t.Errorf("%d sets reached the desk", f.setCount())
	}
	if g, _ := f.value(mustStrip("ch/01").eq(1, bandGain)); g != 0.0 {
		t.Errorf("gain moved to %v", g)
	}
}

func TestUnreadParameterRefusesTheWholeWrite(t *testing.T) {
	f, s := bassDesk(t)
	f.mu.Lock()
	delete(f.vals, mustStrip("ch/01").eq(1, bandQ).addr) // the desk never answers this one
	f.mu.Unlock()
	_, err := s.setEQBand(eqArgs{Strip: "ch/01", Band: 1, GainDB: f64(3), Q: f64(1)})
	if !errors.Is(err, errSilent) || !strings.Contains(err.Error(), "q") {
		t.Fatalf("err = %v", err)
	}
	if f.setCount() != 0 {
		t.Errorf("gain was sent although q could not be read")
	}
}

func TestInvalidArgumentsAreRefusedNotClamped(t *testing.T) {
	f, s := bassDesk(t)
	cases := map[string]func() error{
		"gain +16":      func() error { _, e := s.setEQBand(eqArgs{Strip: "ch/01", Band: 1, GainDB: f64(16)}); return e },
		"freq 10 Hz":    func() error { _, e := s.setEQBand(eqArgs{Strip: "ch/01", Band: 1, FreqHz: f64(10)}); return e },
		"q 11":          func() error { _, e := s.setEQBand(eqArgs{Strip: "ch/01", Band: 1, Q: f64(11)}); return e },
		"band 5":        func() error { _, e := s.setEQBand(eqArgs{Strip: "ch/01", Band: 5, GainDB: f64(1)}); return e },
		"no field":      func() error { _, e := s.setEQBand(eqArgs{Strip: "ch/01", Band: 1}); return e },
		"dca eq":        func() error { _, e := s.setEQBand(eqArgs{Strip: "dca/1", Band: 1, GainDB: f64(1)}); return e },
		"fader +11":     func() error { _, e := s.setFader(faderArgs{Strip: "ch/01", DB: 11}); return e },
		"fader -91":     func() error { _, e := s.setFader(faderArgs{Strip: "ch/01", DB: -91}); return e },
		"ratio 2.2":     func() error { _, e := s.setComp(compArgs{Strip: "ch/01", Ratio: f64(2.2)}); return e },
		"threshold +1":  func() error { _, e := s.setComp(compArgs{Strip: "ch/01", Threshold: f64(1)}); return e },
		"release 4001":  func() error { _, e := s.setComp(compArgs{Strip: "ch/01", ReleaseMs: f64(4001)}); return e },
		"comp on aux":   func() error { _, e := s.setComp(compArgs{Strip: "auxin/01", MakeupDB: f64(1)}); return e },
		"no comp field": func() error { _, e := s.setComp(compArgs{Strip: "ch/01"}); return e },
		"unknown strip": func() error { _, e := s.setMute(muteArgs{Strip: "trombone", Muted: true}); return e },
		"ambiguous":     func() error { _, e := s.setMute(muteArgs{Strip: "bass", Muted: true}); return e },
	}
	for name, call := range cases {
		if err := call(); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	if f.setCount() != 0 {
		t.Errorf("%d sets reached the desk", f.setCount())
	}
}

func TestSetFaderByNameWritesTheRightStrip(t *testing.T) {
	f, s := bassDesk(t)
	res, err := s.setFader(faderArgs{Strip: "BASS gtr", DB: -5})
	if err != nil {
		t.Fatal(err)
	}
	if got := changed(t, res, "fader_db"); got["from"] != 0.0 || got["to"] != -5.0 {
		t.Errorf("fader_db = %v", got)
	}
	if v, _ := f.value(mustStrip("ch/01").fader()); v != -5.0 {
		t.Errorf("desk fader = %v", v)
	}
	if res.(map[string]any)["name"] != "Bass Gtr" {
		t.Errorf("result should name the strip: %v", res)
	}
}

func TestSetMuteAndComp(t *testing.T) {
	f, s := bassDesk(t)
	if _, err := s.setMute(muteArgs{Strip: "ch/01", Muted: true}); err != nil {
		t.Fatal(err)
	}
	if muted, _ := f.value(mustStrip("ch/01").mute()); muted != true {
		t.Errorf("muted = %v", muted)
	}
	if raw := f.vals[mustStrip("ch/01").mute().addr][0]; raw != int32(0) {
		t.Errorf("the desk's on switch = %v, want 0 (mute is on turned round)", raw)
	}
	res, err := s.setComp(compArgs{Strip: "ch/01", Threshold: f64(-20), Ratio: f64(4), On: new(bool)})
	if err != nil {
		t.Fatal(err)
	}
	if got := changed(t, res, "ratio"); got["to"] != 4.0 {
		t.Errorf("ratio = %v", got)
	}
	if got := changed(t, res, "threshold_db"); got["to"] != -20.0 {
		t.Errorf("threshold = %v", got)
	}
}

func TestLinkedStripIsFlagged(t *testing.T) {
	f, s := bassDesk(t)
	_, link, _ := mustStrip("ch/01").pair()
	f.mu.Lock()
	f.vals[link] = []any{int32(1)}
	f.mu.Unlock()
	res, err := s.setMute(muteArgs{Strip: "ch/01", Muted: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["linked_with"] != "ch/02" {
		t.Errorf("want linked_with ch/02: %v", res)
	}
}

func TestGetStripNullsWhatTheDeskDidNotAnswer(t *testing.T) {
	f, s := bassDesk(t)
	f.seed(mustStrip("ch/01").pan(), 0.0)
	res, err := s.getStrip(stripArg{Strip: "ch/01"})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)
	if out["fader_db"] != 0.0 || out["muted"] != false || out["pan"] != 0.0 || out["name"] != "Bass Gtr" {
		t.Errorf("basics: %v", out)
	}
	eq := out["eq"].(map[string]any)
	bands := eq["bands"].([]any)
	band1 := bands[0].(map[string]any)
	if band1["type"] != "PEQ" || band1["freq_hz"] != 101.0 || band1["gain_db"] != 0.0 || band1["q"] != 1.96 { // seeded 100 Hz and Q 2, snapped
		t.Errorf("band 1: %v", band1)
	}
	if eq["on"] != nil || bands[1].(map[string]any)["gain_db"] != nil {
		t.Errorf("unanswered EQ values must be null: %v", eq)
	}
	if out["preamp"] != nil {
		t.Errorf("preamp index unanswered must be null: %v", out["preamp"])
	}
	if _, err := json.Marshal(out); err != nil {
		t.Errorf("not JSON: %v", err)
	}
}

func TestGetStripReadsThePreamp(t *testing.T) {
	f, s := bassDesk(t)
	ch := mustStrip("ch/01")
	f.mu.Lock()
	f.vals[ch.haIndexAddr()] = []any{int32(7)}
	f.mu.Unlock()
	f.seed(headampGain(7), 24.0)
	f.seed(headampPhantom(7), true)
	res, err := s.getStrip(stripArg{Strip: "ch/01"})
	if err != nil {
		t.Fatal(err)
	}
	pre := res.(map[string]any)["preamp"].(map[string]any)
	if pre["gain_db"] != 24.0 || pre["phantom_48v"] != true || pre["headamp"] != int32(7) {
		t.Errorf("preamp: %v", pre)
	}
}

func TestListStrips(t *testing.T) {
	_, s := bassDesk(t)
	res, err := s.listStrips()
	if err != nil {
		t.Fatal(err)
	}
	list := res.(map[string]any)["strips"].([]map[string]any)
	if len(list) != 80 {
		t.Fatalf("%d strips, want 80", len(list))
	}
	if list[0]["id"] != "ch/01" || list[0]["name"] != "Bass Gtr" {
		t.Errorf("first strip: %v", list[0])
	}
	if list[5]["name"] != nil { // ch/06: the desk did not answer
		t.Errorf("an unanswered name must be null: %v", list[5])
	}
}

func TestListStripsFailsWhenSilent(t *testing.T) {
	f, s := bassDesk(t)
	f.setSilent(true)
	if _, err := s.listStrips(); !errors.Is(err, errSilent) {
		t.Errorf("err = %v", err)
	}
}

func TestRefusesAnythingThatIsNotAnM32(t *testing.T) {
	f, desk := newFakeDesk(t)
	f.mu.Lock()
	f.vals["/xinfo"] = []any{"127.0.0.1", "Some", "Behringer Wing", "1.0"}
	f.mu.Unlock()
	if _, err := desk.Model(); err == nil || errors.Is(err, errSilent) {
		t.Errorf("err = %v", err)
	}
}

// The MCP round trip through the SDK's in-memory transport, as Claude Code would call it.
func TestMCPRoundTrip(t *testing.T) {
	f, s := bassDesk(t)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := newMCP(s.desk).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 6 {
		t.Fatalf("tools: %v, %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil { // the SDK may refuse bad arguments as a protocol error
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
		}
		return res
	}
	text := func(r *mcp.CallToolResult) string { return r.Content[0].(*mcp.TextContent).Text }

	res := call("set_eq_band", map[string]any{"strip": "bass gtr", "band": 1, "gain_db": 2.5})
	var out struct {
		Changed map[string]struct{ To float64 }
	}
	if res.IsError || json.Unmarshal([]byte(text(res)), &out) != nil || out.Changed["gain_db"].To != 2.5 {
		t.Fatalf("set_eq_band: %v %s", res.IsError, text(res))
	}
	if res.StructuredContent == nil {
		t.Error("no structuredContent: a client that reads only that sees nothing")
	}
	if g, _ := f.value(mustStrip("ch/01").eq(1, bandGain)); g != 2.5 {
		t.Errorf("desk gain = %v", g)
	}

	sets := f.setCount()
	for name, args := range map[string]map[string]any{
		"gain too high":   {"strip": "ch/01", "band": 1, "gain_db": 40.0},
		"wrong type":      {"strip": "ch/01", "band": 1, "gain_db": "loud"},
		"unknown field":   {"strip": "ch/01", "band": 1, "gain_db": 1.0, "bypass": true},
		"fractional band": {"strip": "ch/01", "band": 1.5, "gain_db": 1.0},
		"missing strip":   {"band": 1, "gain_db": 1.0},
	} {
		if r := call("set_eq_band", args); !r.IsError {
			t.Errorf("%s: want an error, got %s", name, text(r))
		}
	}
	if f.setCount() != sets {
		t.Errorf("a refused call reached the desk")
	}

	f.setSilent(true)
	if r := call("set_fader", map[string]any{"strip": "ch/01", "db": -3.0}); !r.IsError || !strings.Contains(text(r), "desk not answering") {
		t.Errorf("silent desk: %v %s", r.IsError, text(r))
	}
}
