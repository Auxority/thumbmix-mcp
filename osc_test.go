package main

import (
	"bytes"
	"reflect"
	"testing"
)

// Expected bytes follow Thumbmix OSCCodecTests.encodesFloatMessageExactly (Core/Tests/ThumbmixCoreTests).
func TestEncodeFloatExactly(t *testing.T) {
	want := append([]byte("/ch/01/mix/fader\x00\x00\x00\x00"), ",f\x00\x00"...)
	want = append(want, 0x3F, 0x40, 0x00, 0x00)
	got := Message{"/ch/01/mix/fader", []any{float32(0.75)}}.Encode()
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestGetHasNoTypeTag(t *testing.T) {
	want := []byte("/xremote\x00\x00\x00\x00")
	if got := (Message{Addr: "/xremote"}).Encode(); !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestRoundTripEveryType(t *testing.T) {
	for _, s := range []string{"", "a", "abc", "abcd", "Kick Drum"} { // every padding length
		m := Message{"/x", []any{int32(-7), float32(0.25), s}}
		got, err := Decode(m.Encode())
		if err != nil || !reflect.DeepEqual(got, m) {
			t.Fatalf("%q: got %v, %v", s, got, err)
		}
	}
}

func TestDecodeWithoutTypeTag(t *testing.T) {
	got, err := Decode([]byte("/xremote\x00\x00\x00\x00"))
	if err != nil || got.Addr != "/xremote" || len(got.Args) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestDecodeRejectsBadInput(t *testing.T) {
	full := Message{"/ch/01/mix/fader", []any{float32(0.75)}}.Encode()
	if _, err := Decode(full[:len(full)-2]); err == nil {
		t.Error("truncated float should fail")
	}
	if _, err := Decode([]byte("/x\x00\x00,T\x00\x00")); err == nil {
		t.Error("unknown type tag should fail")
	}
	if _, err := Decode([]byte("/x")); err == nil {
		t.Error("unterminated address should fail")
	}
}
