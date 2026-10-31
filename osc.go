package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Message is one OSC message. Args hold int32, float32 or string: all the M32 answers with
// for the parameters this server touches (meter blobs are never requested).
// Hand-rolled because the subset is ~60 lines; a library would add a dependency and features unused here.
type Message struct {
	Addr string
	Args []any
}

var errShort = errors.New("truncated OSC message")

// Encode writes a message. Without arguments it has no type tag: that is how the desk takes a "get"
// (Thumbmix OSCCodec.encode, Core/Sources/ThumbmixCore/OSC/OSCCodec.swift).
func (m Message) Encode() []byte {
	out := appendString(nil, m.Addr)
	if len(m.Args) == 0 {
		return out
	}
	tags, body := ",", []byte(nil)
	for _, a := range m.Args {
		switch v := a.(type) {
		case int32:
			tags, body = tags+"i", binary.BigEndian.AppendUint32(body, uint32(v))
		case float32:
			tags, body = tags+"f", binary.BigEndian.AppendUint32(body, math.Float32bits(v))
		case string:
			tags, body = tags+"s", appendString(body, v)
		default:
			panic(fmt.Sprintf("unsupported OSC argument %T", a))
		}
	}
	return append(appendString(out, tags), body...)
}

func appendString(out []byte, s string) []byte {
	out = append(out, s...)
	out = append(out, 0)
	for len(out)%4 != 0 {
		out = append(out, 0)
	}
	return out
}

// Decode reads a message; a message without a type tag has no arguments.
func Decode(b []byte) (Message, error) {
	addr, b, err := readString(b)
	if err != nil {
		return Message{}, err
	}
	m := Message{Addr: addr}
	if len(b) == 0 || b[0] != ',' {
		return m, nil
	}
	tags, b, err := readString(b)
	if err != nil {
		return Message{}, err
	}
	for _, tag := range tags[1:] {
		switch tag {
		case 'i', 'f':
			if len(b) < 4 {
				return Message{}, errShort
			}
			u := binary.BigEndian.Uint32(b)
			b = b[4:]
			if tag == 'i' {
				m.Args = append(m.Args, int32(u))
			} else {
				m.Args = append(m.Args, math.Float32frombits(u))
			}
		case 's':
			var s string
			if s, b, err = readString(b); err != nil {
				return Message{}, err
			}
			m.Args = append(m.Args, s)
		default:
			return Message{}, fmt.Errorf("unsupported OSC type tag %q", tag)
		}
	}
	return m, nil
}

func readString(b []byte) (string, []byte, error) {
	for i, c := range b {
		if c == 0 {
			end := min((i+4)&^3, len(b))
			return string(b[:i]), b[end:], nil
		}
	}
	return "", nil, errShort
}
