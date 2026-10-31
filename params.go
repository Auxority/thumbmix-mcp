package main

import (
	"fmt"
	"math"
	"slices"
)

// scale maps the desk's 0...1 float to real units. The desk snaps every value to a grid of `steps`
// positions (Thumbmix ParamScale, Core/Sources/ThumbmixCore/M32/ParamScale.swift).
type scale struct {
	lo, hi float64
	steps  int
	log    bool
	unit   string
}

// Ranges and grids from Maillot's protocol doc v4.09 as ported in Thumbmix Catalog.swift (doc p.25-26, 43).
var (
	eqFreq     = scale{20, 20000, 201, true, "Hz"}
	eqGain     = scale{-15, 15, 121, false, "dB"} // step 0.25
	eqQ        = scale{10, 0.3, 72, true, ""}     // runs high to low
	compThr    = scale{-60, 0, 121, false, "dB"}  // step 0.5
	compMake   = scale{0, 24, 49, false, "dB"}    // step 0.5
	compAtt    = scale{0, 120, 121, false, "ms"}  // step 1
	compRel    = scale{5, 4000, 101, true, "ms"}
	panScale   = scale{-100, 100, 101, false, ""} // step 2
	headGain   = scale{-12, 60, 145, false, "dB"} // step 0.5
	faderRange = scale{lo: faderFloor, hi: 10, unit: "dB"}
	compRatio  = []any{1.1, 1.3, 1.5, 2.0, 2.5, 3.0, 4.0, 5.0, 7.0, 10.0, 20.0, 100.0}
	eqTypes    = []any{"LCut", "LShv", "PEQ", "VEQ", "HShv", "HCut", "BU6", "BU12", "BS12", "LR12", "BU18", "BU24", "BS24", "LR24"}
	dynModes   = []any{"COMP", "EXP"}
)

func clamp01(w float64) float64 { return math.Min(math.Max(w, 0), 1) }

func snap(w float64, steps int) float32 {
	n := float64(steps - 1)
	return float32(math.Round(clamp01(w)*n) / n)
}

func (s scale) value(w float64) float64 {
	w = clamp01(w)
	if s.log {
		return sig3(s.lo * math.Pow(s.hi/s.lo, w))
	}
	return math.Round((s.lo+w*(s.hi-s.lo))*100) / 100
}

func (s scale) wire(v float64) float32 {
	if s.log {
		return snap(math.Log(v/s.lo)/math.Log(s.hi/s.lo), s.steps)
	}
	return snap((v-s.lo)/(s.hi-s.lo), s.steps)
}

func (s scale) check(name string, v float64) error {
	lo, hi := math.Min(s.lo, s.hi), math.Max(s.lo, s.hi)
	if v < lo || v > hi || math.IsNaN(v) {
		return fmt.Errorf("%s %v is out of range (%v to %v %s); nothing was sent", name, v, lo, hi, s.unit)
	}
	return nil
}

func sig3(v float64) float64 {
	if v == 0 {
		return 0
	}
	f := math.Pow(10, 2-math.Floor(math.Log10(math.Abs(v))))
	return math.Round(v*f) / f
}

const faderFloor = -90.0

// faderDB is the 4-segment fader taper (Maillot doc p.131, Thumbmix FaderLaw.swift): 0.75 = 0 dB, 1.0 = +10 dB.
// Wire 0 is -infinity, which JSON cannot carry: it reads -90, and 1/1023 reads -89.5.
// The desk shows a detent at unity: steps 765-769 read 0 (FaderLaw.deskExceptions, doc p.145).
func faderDB(w float64) float64 {
	step := int(math.Round(clamp01(w) * 1023))
	var db float64
	switch {
	case step >= 765 && step <= 769:
		return 0
	case w <= 0:
		return faderFloor
	case w >= 0.5:
		db = w*40 - 30
	case w >= 0.25:
		db = w*80 - 50
	case w >= 0.0625:
		db = w*160 - 70
	default:
		db = w*480 - 90
	}
	return math.Round(db*10) / 10
}

func faderWire(db float64) float32 {
	var w float64
	switch {
	case db < -90:
		w = 0
	case db < -60:
		w = (db + 90) / 480
	case db < -30:
		w = (db + 70) / 160
	case db < -10:
		w = (db + 50) / 80
	default:
		w = math.Min((db+30)/40, 1)
	}
	return snap(w, 1024)
}

type pkind int

const (
	pFader pkind = iota
	pLevel
	pToggle
	pChoice
)

// param is one desk address and how its argument converts to and from real units.
type param struct {
	name string // key in tool output
	addr string
	kind pkind
	sc   scale // pLevel
	opts []any // pChoice: the real value of each index
	inv  bool  // pToggle: true reads as 0 ("muted" is the desk's "on" turned round)
}

// check refuses a value the desk cannot hold; nothing is clamped.
func (p param) check(v float64) error {
	switch p.kind {
	case pFader:
		return faderRange.check(p.name, v)
	case pChoice:
		if !slices.Contains(p.opts, any(v)) {
			return fmt.Errorf("%s %v is not one of %v; nothing was sent", p.name, v, p.opts)
		}
		return nil
	}
	return p.sc.check(p.name, v)
}

// decode turns the desk's argument into a real value; false when it has the wrong type.
func (p param) decode(arg any) (any, bool) {
	switch v := arg.(type) {
	case float32:
		switch p.kind {
		case pFader:
			return faderDB(float64(v)), true
		case pLevel:
			return p.sc.value(float64(v)), true
		}
	case int32:
		switch p.kind {
		case pToggle:
			return (v == 1) != p.inv, true
		case pChoice:
			if v >= 0 && int(v) < len(p.opts) {
				return p.opts[v], true
			}
		}
	}
	return nil, false
}

// encode turns a validated real value into the desk's argument.
func (p param) encode(v any) any {
	switch p.kind {
	case pFader:
		return faderWire(v.(float64))
	case pLevel:
		return p.sc.wire(v.(float64))
	case pToggle:
		if v.(bool) != p.inv {
			return int32(1)
		}
		return int32(0)
	}
	for i, o := range p.opts {
		if o == v {
			return int32(i)
		}
	}
	panic(fmt.Sprintf("%s: %v is not an option", p.name, v))
}
