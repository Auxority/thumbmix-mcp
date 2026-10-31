package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// kind is one family of strips on the desk, as Thumbmix StripKind/StripID list them
// (Core/Sources/ThumbmixCore/M32/StripID.swift; doc p.25-43).
type kind struct {
	format   string // OSC prefix; %02d takes the strip number (DCAs are the only unpadded ones)
	label    string // the desk's default name, with the number appended
	count    int
	bands    int    // EQ bands; 0 = no EQ
	dyn, pan bool   // has a comp, has a pan
	link     string // /config/<link>/1-2 switches a stereo pair; "" = cannot link
	preamp   bool   // fed by a headamp (inputs; aux ins are inputs 33-40)
	haBase   int
}

var kinds = []kind{
	{"/ch/%02d", "Ch", 32, 4, true, true, "chlink", true, 0},
	{"/auxin/%02d", "Aux", 8, 4, false, true, "auxlink", true, 32},
	{"/fxrtn/%02d", "FX", 8, 4, false, true, "fxlink", false, 0},
	{"/bus/%02d", "Bus", 16, 6, true, true, "buslink", false, 0},
	{"/mtx/%02d", "Matrix", 6, 6, true, false, "mtxlink", false, 0},
	{"/main/st", "Main LR", 1, 6, true, true, "", false, 0},
	{"/main/m", "Main M", 1, 6, true, false, "", false, 0},
	{"/dca/%d", "DCA", 8, 0, false, false, "", false, 0},
}

type strip struct{ kind, n int } // n is 1-based, as printed on the desk

func allStrips() []strip {
	var all []strip
	for i, k := range kinds {
		for n := 1; n <= k.count; n++ {
			all = append(all, strip{i, n})
		}
	}
	return all
}

func (s strip) k() kind { return kinds[s.kind] }

func (s strip) prefix() string {
	if f := s.k().format; strings.Contains(f, "%") {
		return fmt.Sprintf(f, s.n)
	}
	return s.k().format
}

func (s strip) id() string { return s.prefix()[1:] }

func (s strip) defaultName() string {
	if s.k().count == 1 {
		return s.k().label
	}
	return fmt.Sprintf("%s %d", s.k().label, s.n)
}

func (s strip) nameAddr() string { return s.prefix() + "/config/name" }

// partner is the other side of a stereo pair; false where the desk cannot link.
func (s strip) partner() (strip, bool) {
	if s.k().link == "" {
		return s, false
	}
	if s.n%2 == 0 {
		return strip{s.kind, s.n - 1}, true
	}
	return strip{s.kind, s.n + 1}, true
}

func (s strip) linkAddr() string {
	if s.k().link == "" {
		return ""
	}
	odd := s.n - (s.n+1)%2
	return fmt.Sprintf("/config/%s/%d-%d", s.k().link, odd, odd+1)
}

// A DCA's level and switch sit at /dca/N/fader and /dca/N/on, the others' under /mix (Thumbmix StripID.fader, .on).
func (s strip) mix() string {
	if s.k().format == "/dca/%d" {
		return s.prefix()
	}
	return s.prefix() + "/mix"
}

func (s strip) fader() param {
	return param{name: "fader_db", addr: s.mix() + "/fader", kind: pFader}
}

// mute is the desk's "on" switch turned round: on = 1 is live, 0 is muted.
func (s strip) mute() param {
	return param{name: "muted", addr: s.mix() + "/on", kind: pToggle, inv: true}
}

func (s strip) pan() param {
	return param{name: "pan", addr: s.prefix() + "/mix/pan", kind: pLevel, sc: panScale}
}

func (s strip) eqOn() param {
	return param{name: "on", addr: s.prefix() + "/eq/on", kind: pToggle}
}

var eqFields = map[string]param{
	"type": {name: "type", kind: pChoice, opts: eqTypes},
	"f":    {name: "freq_hz", kind: pLevel, sc: eqFreq},
	"g":    {name: "gain_db", kind: pLevel, sc: eqGain},
	"q":    {name: "q", kind: pLevel, sc: eqQ},
}

var dynFields = map[string]param{
	"on":      {name: "on", kind: pToggle},
	"mode":    {name: "mode", kind: pChoice, opts: dynModes},
	"thr":     {name: "threshold_db", kind: pLevel, sc: compThr},
	"ratio":   {name: "ratio", kind: pChoice, opts: compRatio},
	"attack":  {name: "attack_ms", kind: pLevel, sc: compAtt},
	"release": {name: "release_ms", kind: pLevel, sc: compRel},
	"mgain":   {name: "makeup_db", kind: pLevel, sc: compMake},
}

func (s strip) eq(band int, field string) param {
	p := eqFields[field]
	p.addr = fmt.Sprintf("%s/eq/%d/%s", s.prefix(), band, field)
	return p
}

func (s strip) dyn(field string) param {
	p := dynFields[field]
	p.addr = s.prefix() + "/dyn/" + field
	return p
}

// haIndexAddr answers which headamp (0-127) feeds the strip, or -1 for an internal source (doc p.43).
func (s strip) haIndexAddr() string {
	return fmt.Sprintf("/-ha/%02d/index", s.k().haBase+s.n-1)
}

func headampGain(i int) param {
	return param{name: "gain_db", addr: fmt.Sprintf("/headamp/%03d/gain", i), kind: pLevel, sc: headGain}
}

func headampPhantom(i int) param {
	return param{name: "phantom_48v", addr: fmt.Sprintf("/headamp/%03d/phantom", i), kind: pToggle}
}

var accents = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a", "ç", "c",
	"é", "e", "è", "e", "ê", "e", "ë", "e", "í", "i", "ì", "i", "î", "i", "ï", "i", "ñ", "n",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o", "ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ý", "y", "ÿ", "y", "ß", "ss")

// fold makes names comparable: lower case, no diacritics, single spaces.
func fold(s string) string {
	return strings.Join(strings.Fields(accents.Replace(strings.ToLower(s))), " ")
}

// canon drops the zero padding of an id's number: "ch/01" and "ch/1" compare equal.
func canon(id string) string {
	i := strings.LastIndex(id, "/")
	if n, err := strconv.Atoi(id[i+1:]); err == nil {
		return id[:i+1] + strconv.Itoa(n)
	}
	return id
}

// idOf finds a strip by its id ("ch/01", "/ch/1", "main/st").
func idOf(ref string) (strip, bool) {
	ref = canon(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ref)), "/"))
	for _, s := range allStrips() {
		if ref == canon(s.id()) {
			return s, true
		}
	}
	return strip{}, false
}

// pick finds a strip by the names the desk gave. An exact name wins; otherwise a name that contains the text
// must be unique. Strips without a name only match by their default name ("Ch 3"), exactly.
func pick(ref string, names map[strip]string) (strip, error) {
	want := fold(ref)
	var exact, partial []strip
	for _, s := range allStrips() {
		name, ok := names[s]
		if !ok {
			continue
		}
		n := fold(name)
		if n == "" {
			n = fold(s.defaultName())
		} else if strings.Contains(n, want) && n != want {
			partial = append(partial, s)
		}
		if n == want {
			exact = append(exact, s)
		}
	}
	found := exact
	if len(found) == 0 {
		found = partial
	}
	switch len(found) {
	case 0:
		return strip{}, fmt.Errorf("no strip is named %q (use list_strips)", ref)
	case 1:
		return found[0], nil
	}
	var c []string
	for _, s := range found {
		c = append(c, fmt.Sprintf("%s (%s)", s.id(), names[s]))
	}
	sort.Strings(c)
	return strip{}, fmt.Errorf("%q is ambiguous: %s; use the id", ref, strings.Join(c, ", "))
}
