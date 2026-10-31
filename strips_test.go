package main

import (
	"strings"
	"testing"
)

func TestStripAddresses(t *testing.T) {
	for _, c := range []struct {
		id, fader, on, link, ha string
	}{
		{"ch/05", "/ch/05/mix/fader", "/ch/05/mix/on", "/config/chlink/5-6", "/-ha/04/index"},
		{"ch/06", "/ch/06/mix/fader", "/ch/06/mix/on", "/config/chlink/5-6", "/-ha/05/index"},
		{"auxin/02", "/auxin/02/mix/fader", "/auxin/02/mix/on", "/config/auxlink/1-2", "/-ha/33/index"},
		{"main/st", "/main/st/mix/fader", "/main/st/mix/on", "", ""},
		{"dca/3", "/dca/3/fader", "/dca/3/on", "", ""}, // DCAs are not zero-padded
	} {
		s, ok := idOf(c.id)
		if !ok {
			t.Fatalf("%s not found", c.id)
		}
		_, link, _ := s.pair()
		if s.fader().addr != c.fader || s.mute().addr != c.on || link != c.link {
			t.Errorf("%s: %s %s %s", c.id, s.fader().addr, s.mute().addr, link)
		}
		if s.k.input > 0 && s.haIndexAddr() != c.ha {
			t.Errorf("%s: headamp index %s, want %s", c.id, s.haIndexAddr(), c.ha)
		}
	}
}

func TestStripCountsMatchThumbmix(t *testing.T) {
	counts := map[string]int{}
	for _, s := range allStrips {
		counts[strings.Split(s.id(), "/")[0]]++
	}
	want := map[string]int{"ch": 32, "auxin": 8, "fxrtn": 8, "bus": 16, "mtx": 6, "main": 2, "dca": 8}
	for k, n := range want {
		if counts[k] != n {
			t.Errorf("%s: %d strips, want %d", k, counts[k], n)
		}
	}
}

func TestIDsAcceptPaddingAndSlash(t *testing.T) {
	for _, ref := range []string{"ch/01", "/ch/1", " CH/01 "} {
		if s, ok := idOf(ref); !ok || s.id() != "ch/01" {
			t.Errorf("%q: got %v %v", ref, s, ok)
		}
	}
	if _, ok := idOf("ch/33"); ok {
		t.Error("ch/33 does not exist")
	}
	if s, ok := idOf("dca/03"); !ok || s.id() != "dca/3" {
		t.Errorf("dca/03: %v %v", s, ok)
	}
}

func named(pairs ...string) map[strip]string {
	names := map[strip]string{}
	for i := 0; i < len(pairs); i += 2 {
		s, _ := idOf(pairs[i])
		names[s] = pairs[i+1]
	}
	return names
}

func TestPickByName(t *testing.T) {
	names := named("ch/01", "Kick", "ch/02", "Bass Gtr", "ch/03", "Bass DI", "ch/04", "Café", "ch/05", "Vox", "ch/06", "Vox Back", "ch/07", "")
	for _, c := range []struct{ ref, want string }{
		{"kick", "ch/01"},
		{"  BASS   gtr ", "ch/02"},
		{"cafe", "ch/04"},
		{"CAFÉ", "ch/04"},
		{"vox", "ch/05"},  // an exact name wins over "Vox Back"
		{"back", "ch/06"}, // a unique part of a name
		{"ch 7", "ch/07"}, // an unnamed strip answers to its default name
		{"Bass Gtr", "ch/02"},
	} {
		s, err := pick(c.ref, names)
		if err != nil || s.id() != c.want {
			t.Errorf("%q: got %v, %v; want %s", c.ref, s.id(), err, c.want)
		}
	}
}

func TestPickRefusesAmbiguousAndUnknown(t *testing.T) {
	names := named("ch/02", "Bass Gtr", "ch/03", "Bass DI", "ch/08", "Snare", "ch/09", "Snare")
	_, err := pick("bass", names)
	if err == nil || !strings.Contains(err.Error(), "ch/02 (Bass Gtr)") || !strings.Contains(err.Error(), "ch/03 (Bass DI)") {
		t.Errorf("ambiguous partial: %v", err)
	}
	if _, err := pick("snare", names); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("two exact names: %v", err)
	}
	if _, err := pick("trombone", names); err == nil || !strings.Contains(err.Error(), "no strip") {
		t.Errorf("unknown: %v", err)
	}
}

func TestPartner(t *testing.T) {
	for a, b := range map[string]string{"bus/03": "bus/04", "bus/04": "bus/03", "ch/32": "ch/31"} {
		if p, _, ok := mustStrip(a).pair(); !ok || p.id() != b {
			t.Errorf("partner of %s = %v, want %s", a, p.id(), b)
		}
	}
	if _, _, ok := mustStrip("dca/1").pair(); ok {
		t.Error("DCAs cannot link")
	}
}

func mustStrip(id string) strip {
	s, ok := idOf(id)
	if !ok {
		panic(id)
	}
	return s
}
