package main

import (
	"math"
	"testing"
)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// Breakpoints from Thumbmix ParamScaleTests.faderLawMatchesDocBreakpoints (doc p.131).
func TestFaderLawBreakpoints(t *testing.T) {
	for _, c := range []struct{ wire, db float64 }{
		{0.75, 0}, {0.825, 3}, {0.5, -10}, {0.25, -30}, {0.0625, -60}, {1, 10},
	} {
		if got := faderDB(c.wire); !near(got, c.db, 0.001) {
			t.Errorf("faderDB(%v) = %v, want %v", c.wire, got, c.db)
		}
		if got := float64(faderWire(c.db)); !near(got, c.wire, 1.0/1023) {
			t.Errorf("faderWire(%v) = %v, want %v", c.db, got, c.wire)
		}
	}
}

// Thumbmix ParamScaleTests.faderUnitySnapsToConsoleGrid: 0 dB is step 767 of 1023.
func TestFaderUnitySnapsToConsoleGrid(t *testing.T) {
	if got, want := faderWire(0), float32(767)/1023; got != want {
		t.Errorf("faderWire(0) = %v, want %v", got, want)
	}
}

// Thumbmix ParamScaleTests.faderBottomIsMinusInfinity: wire 0 is -inf, here -90 because JSON has no infinity.
func TestFaderBottom(t *testing.T) {
	if faderDB(0) != faderFloor || faderWire(-90) != 0 || faderWire(-200) != 0 {
		t.Error("the bottom of the fader should be -90 and wire 0")
	}
	if got := faderDB(1.0 / 1023); got != -89.5 {
		t.Errorf("step 1 = %v dB, want -89.5 (doc p.145)", got)
	}
}

// Thumbmix FaderLaw.deskExceptions: steps 765-769 read 0 dB, a detent at unity.
func TestFaderUnityDetent(t *testing.T) {
	for step := 765; step <= 769; step++ {
		if got := faderDB(float64(step) / 1023); got != 0 {
			t.Errorf("step %d = %v dB, want 0", step, got)
		}
	}
}

// Thumbmix ParamScaleTests.headampGainMapping: 24 dB is the middle of -12...60.
func TestHeadampGain(t *testing.T) {
	if got := headGain.wire(24); got != 0.5 {
		t.Errorf("wire(24) = %v, want 0.5", got)
	}
	if got := headGain.value(0.5); got != 24 {
		t.Errorf("value(0.5) = %v, want 24", got)
	}
}

// Thumbmix ParamScaleTests.logFrequencyMatchesDoc: 0.5 is 632.46 Hz, 1 is 20 kHz.
func TestEQFreq(t *testing.T) {
	if got := eqFreq.value(0.5); !near(got, 632, 0.5) { // shown to 3 significant digits
		t.Errorf("value(0.5) = %v, want 632", got)
	}
	if got := eqFreq.value(1); got != 20000 {
		t.Errorf("value(1) = %v", got)
	}
	if got := float64(eqFreq.wire(632.46)); !near(got, 0.5, 0.0001) {
		t.Errorf("wire(632.46) = %v", got)
	}
}

// Thumbmix ParamScaleTests.logQRunsHighToLow: Q is 10 at wire 0 and 0.3 at wire 1.
func TestEQQRunsHighToLow(t *testing.T) {
	if eqQ.value(0) != 10 || eqQ.value(1) != 0.3 {
		t.Errorf("Q ends are %v and %v", eqQ.value(0), eqQ.value(1))
	}
	if eqQ.wire(10) != 0 || eqQ.wire(0.3) != 1 {
		t.Error("Q wire ends")
	}
}

// Linear scales, with the doc's steps (Catalog.swift): a gain of 0 is the middle, 0.25 dB per step.
func TestLinearScales(t *testing.T) {
	if eqGain.wire(0) != 0.5 || eqGain.value(1) != 15 || eqGain.value(0) != -15 {
		t.Error("eq gain")
	}
	if got := float64(eqGain.wire(3.1)); !near(got, 0.6, 1e-6) { // snaps 3.1 to 3.0 dB = 18/30
		t.Errorf("3.1 dB should snap to 3 dB, got wire %v", got)
	}
	if compThr.wire(-60) != 0 || compThr.value(1) != 0 || compMake.value(1) != 24 || compAtt.value(1) != 120 {
		t.Error("comp scales")
	}
	if got := compRel.value(1); got != 4000 {
		t.Errorf("release max = %v", got)
	}
}

// Thumbmix ParamScaleTests.choiceTravelsAsInt / toggleTravelsAsInt.
func TestChoiceAndToggleTravelAsInt(t *testing.T) {
	ratio := param{kind: pChoice, opts: compRatio}
	if got := ratio.encode(2.0); got != int32(3) {
		t.Errorf("ratio 2.0 encodes %v, want int 3", got)
	}
	if v, ok := ratio.decode(int32(3)); !ok || v != 2.0 {
		t.Errorf("index 3 decodes %v, %v", v, ok)
	}
	on := param{kind: pToggle}
	if on.encode(true) != int32(1) || on.encode(false) != int32(0) {
		t.Error("toggle encode")
	}
	if v, ok := on.decode(int32(0)); !ok || v != false {
		t.Error("toggle decode")
	}
}

// Thumbmix ParamScaleTests.wrongArgumentTypeIsNil.
func TestDecodeWrongType(t *testing.T) {
	if _, ok := (param{kind: pFader}).decode("x"); ok {
		t.Error("string is not a fader")
	}
	if _, ok := (param{kind: pToggle}).decode(float32(1)); ok {
		t.Error("float is not a toggle")
	}
	if _, ok := (param{kind: pChoice, opts: eqTypes}).decode(int32(99)); ok {
		t.Error("index 99 is not an EQ type")
	}
}

func TestCheckRefusesOutOfRange(t *testing.T) {
	for _, v := range []float64{-15.1, 15.25, math.NaN()} {
		if eqGain.check("gain_db", v) == nil {
			t.Errorf("%v should be refused", v)
		}
	}
	if eqGain.check("gain_db", 15) != nil || eqQ.check("q", 0.3) != nil || eqQ.check("q", 10) != nil {
		t.Error("range ends are allowed")
	}
}
