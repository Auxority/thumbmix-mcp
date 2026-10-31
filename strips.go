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
	format string // OSC prefix; %02d takes the strip number (DCAs are the only unpadded ones)
	label  string // the desk's default name, with the number appended when there are several
	count  int
	mix    string // where the fader and on switch sit under the prefix; a DCA has them at the top (StripID.fader, .on)
	bands  int    // EQ bands; 0 = no EQ
	comp   bool
	pan    bool
	link   string // /config/<link>/1-2 switches a stereo pair; "" = cannot link
	input  int    // the input number of strip 1, fed by a headamp (aux ins are inputs 33-40); 0 = no preamp
}

var kinds = []kind{
	{format: "/ch/%02d", label: "Ch", count: 32, mix: "/mix", bands: 4, comp: true, pan: true, link: "chlink", input: 1},
	{format: "/auxin/%02d", label: "Aux", count: 8, mix: "/mix", bands: 4, pan: true, link: "auxlink", input: 33},
	{format: "/fxrtn/%02d", label: "FX", count: 8, mix: "/mix", bands: 4, pan: true, link: "fxlink"},
	{format: "/bus/%02d", label: "Bus", count: 16, mix: "/mix", bands: 6, comp: true, pan: true, link: "buslink"},
	{format: "/mtx/%02d", label: "Matrix", count: 6, mix: "/mix", bands: 6, comp: true, link: "mtxlink"},
	{format: "/main/st", label: "Main LR", count: 1, mix: "/mix", bands: 6, comp: true, pan: true},
	{format: "/main/m", label: "Main M", count: 1, mix: "/mix", bands: 6, comp: true},
	{format: "/dca/%d", label: "DCA", count: 8},
}

type strip struct {
	k *kind
	n int // 1-based, as printed on the desk
}

func allStrips() []strip {
	var all []strip
	for i := range kinds {
		for n := 1; n <= kinds[i].count; n++ {
			all = append(all, strip{&kinds[i], n})
		}
	}
	return all
}

func (s strip) prefix() string {
	if s.k.count == 1 {
		return s.k.format
	}
	return fmt.Sprintf(s.k.format, s.n)
}

func (s strip) id() string { return s.prefix()[1:] }

func (s strip) defaultName() string {
	if s.k.count == 1 {
		return s.k.label
	}
	return fmt.Sprintf("%s %d", s.k.label, s.n)
}

func (s strip) nameAddr() string { return s.prefix() + "/config/name" }

// pair is the other side of the stereo pair and the address that says whether the two are linked;
// false where the desk cannot link.
func (s strip) pair() (partner strip, linkAddr string, ok bool) {
	if s.k.link == "" {
		return strip{}, "", false
	}
	odd := s.n - (s.n+1)%2
	partner = strip{s.k, odd + 1}
	if s.n == odd+1 {
		partner.n = odd
	}
	return partner, fmt.Sprintf("/config/%s/%d-%d", s.k.link, odd, odd+1), true
}

// headerAddrs are read with every answer about the strip: its name, and whether it is linked.
func (s strip) headerAddrs() []string {
	addrs := []string{s.nameAddr()}
	if _, link, ok := s.pair(); ok {
		addrs = append(addrs, link)
	}
	return addrs
}

func (s strip) mixAddr() string { return s.prefix() + s.k.mix }

func (s strip) fader() param {
	return param{"fader_db", s.mixAddr() + "/fader", faderLaw{}}
}

// mute is the desk's "on" switch turned round: on = 1 is live, 0 is muted.
func (s strip) mute() param {
	return param{"muted", s.mixAddr() + "/on", toggle{inv: true}}
}

func (s strip) pan() param {
	return param{"pan", s.mixAddr() + "/pan", panScale}
}

func (s strip) eqOn() param {
	return param{"on", s.prefix() + "/eq/on", toggle{}}
}

// field is one EQ band or comp parameter: its OSC key under the strip, its name in tool output, its codec.
type field struct {
	key, name string
	codec     codec
}

var (
	bandType   = field{"type", "type", eqTypes}
	bandFreq   = field{"f", "freq_hz", eqFreq}
	bandGain   = field{"g", "gain_db", eqGain}
	bandQ      = field{"q", "q", eqQ}
	dynOn      = field{"on", "on", toggle{}}
	dynMode    = field{"mode", "mode", dynModes}
	dynThr     = field{"thr", "threshold_db", compThr}
	dynRatio   = field{"ratio", "ratio", compRatio}
	dynAttack  = field{"attack", "attack_ms", compAtt}
	dynRelease = field{"release", "release_ms", compRel}
	dynMakeup  = field{"mgain", "makeup_db", compMake}
	bandFields = []field{bandType, bandFreq, bandGain, bandQ}
	dynFields  = []field{dynOn, dynMode, dynThr, dynRatio, dynAttack, dynRelease, dynMakeup}
)

func (s strip) eq(band int, f field) param {
	return param{f.name, fmt.Sprintf("%s/eq/%d/%s", s.prefix(), band, f.key), f.codec}
}

func (s strip) dyn(f field) param {
	return param{f.name, s.prefix() + "/dyn/" + f.key, f.codec}
}

// haIndexAddr answers which headamp (0-127) feeds the strip, or -1 for an internal source (doc p.43).
func (s strip) haIndexAddr() string {
	return fmt.Sprintf("/-ha/%02d/index", s.k.input+s.n-2)
}

func headampGain(i int) param {
	return param{"gain_db", fmt.Sprintf("/headamp/%03d/gain", i), headGain}
}

func headampPhantom(i int) param {
	return param{"phantom_48v", fmt.Sprintf("/headamp/%03d/phantom", i), toggle{}}
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
