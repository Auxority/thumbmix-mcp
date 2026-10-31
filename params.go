package main

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// codec turns a parameter's desk argument into a real value and back. encode refuses what the desk
// cannot hold (nothing is clamped); decode is false for an argument of the wrong type.
type codec interface {
	decode(arg any) (any, bool)
	encode(v any) (any, error)
}

// param is one desk address and how its argument converts. A param without a codec panics on first use.
type param struct {
	name string // key in tool output
	addr string
	codec
}

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
	eqFreq    = scale{20, 20000, 201, true, "Hz"}
	eqGain    = scale{-15, 15, 121, false, "dB"} // step 0.25
	eqQ       = scale{10, 0.3, 72, true, ""}     // runs high to low
	compThr   = scale{-60, 0, 121, false, "dB"}  // step 0.5
	compMake  = scale{0, 24, 49, false, "dB"}    // step 0.5
	compAtt   = scale{0, 120, 121, false, "ms"}  // step 1
	compRel   = scale{5, 4000, 101, true, "ms"}
	panScale  = scale{-100, 100, 101, false, ""} // step 2
	headGain  = scale{-12, 60, 145, false, "dB"} // step 0.5
	compRatio = choice[float64]{1.1, 1.3, 1.5, 2.0, 2.5, 3.0, 4.0, 5.0, 7.0, 10.0, 20.0, 100.0}
	eqTypes   = choice[string]{"LCut", "LShv", "PEQ", "VEQ", "HShv", "HCut", "BU6", "BU12", "BS12", "LR12", "BU18", "BU24", "BS24", "LR24"}
	dynModes  = choice[string]{"COMP", "EXP"}
)

func (s scale) decode(arg any) (any, bool) {
	w, ok := arg.(float32)
	if !ok {
		return nil, false
	}
	return s.value(float64(w)), true
}

func (s scale) encode(v any) (any, error) {
	f, err := number(v, s.lo, s.hi, s.unit)
	if err != nil {
		return nil, err
	}
	return s.wire(f), nil
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

// number refuses anything but a number from lo to hi (in either order).
func number(v any, lo, hi float64, unit string) (float64, error) {
	f, ok := v.(float64)
	if !ok {
		return 0, errors.New("is not a number")
	}
	lo, hi = min(lo, hi), max(lo, hi)
	if !(f >= lo && f <= hi) { // NaN fails too
		return 0, fmt.Errorf("is out of range (%v to %v %s)", lo, hi, unit)
	}
	return f, nil
}

func clamp01(w float64) float64 { return min(max(w, 0), 1) }

func snap(w float64, steps int) float32 {
	n := float64(steps - 1)
	return float32(math.Round(clamp01(w)*n) / n)
}

func sig3(v float64) float64 {
	if v == 0 {
		return 0
	}
	f := math.Pow(10, 2-math.Floor(math.Log10(math.Abs(v))))
	return math.Round(v*f) / f
}

// toggle is the desk's 0/1 switch; inv reads 1 as false ("muted" is the desk's "on" turned round).
type toggle struct{ inv bool }

func (t toggle) decode(arg any) (any, bool) {
	i, ok := arg.(int32)
	if !ok {
		return nil, false
	}
	return (i == 1) != t.inv, true
}

func (t toggle) encode(v any) (any, error) {
	b, ok := v.(bool)
	if !ok {
		return nil, errors.New("is not true or false")
	}
	if b != t.inv {
		return int32(1), nil
	}
	return int32(0), nil
}

// choice is the desk's list of options; the wire carries the index.
type choice[T comparable] []T

func (c choice[T]) decode(arg any) (any, bool) {
	i, ok := arg.(int32)
	if !ok || i < 0 || int(i) >= len(c) {
		return nil, false
	}
	return c[i], true
}

func (c choice[T]) encode(v any) (any, error) {
	if t, ok := v.(T); ok {
		if i := slices.Index(c, t); i >= 0 {
			return int32(i), nil
		}
	}
	return nil, fmt.Errorf("is not one of %v", []T(c))
}

const faderFloor = -90.0

// faderLaw is the fader's codec: its 4-segment taper is not a scale.
type faderLaw struct{}

func (faderLaw) decode(arg any) (any, bool) {
	w, ok := arg.(float32)
	if !ok {
		return nil, false
	}
	return faderDB(float64(w)), true
}

func (faderLaw) encode(v any) (any, error) {
	db, err := number(v, faderFloor, 10, "dB")
	if err != nil {
		return nil, err
	}
	return faderWire(db), nil
}

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
