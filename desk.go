package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// Desk talks OSC over UDP to one console. The desk answers a "get" (the address with no arguments)
// with the same address and the value; it stays silent for an address it does not know.
type Desk struct {
	mu      sync.Mutex
	conn    net.Conn
	timeout time.Duration // how long Get waits in total before giving up on a value
}

// Reply holds the arguments of each answer, by address.
type Reply map[string][]any

var errSilent = errors.New("desk not answering")

func Dial(addr string, timeout time.Duration) (*Desk, error) {
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return nil, err
	}
	return &Desk{conn: conn, timeout: timeout}, nil
}

// Get asks for every address and returns what came back. Missing addresses are asked again twice;
// whatever never answers is absent from the result.
func (d *Desk) Get(addrs ...string) Reply {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.drain()
	got := Reply{}
	missing := map[string]bool{}
	for _, a := range addrs {
		if a != "" { // strips that cannot link have no link address
			missing[a] = true
		}
	}
	buf := make([]byte, 2048)
	for round := 0; round < 3 && len(missing) > 0; round++ {
		for a := range missing {
			if _, err := d.conn.Write(Message{Addr: a}.Encode()); err != nil {
				slog.Warn("send to desk failed", "addr", a, "err", err)
			}
		}
		_ = d.conn.SetReadDeadline(time.Now().Add(d.timeout / 3))
		for len(missing) > 0 {
			n, err := d.conn.Read(buf)
			if err != nil {
				break
			}
			if m, err := Decode(buf[:n]); err == nil && missing[m.Addr] && len(m.Args) > 0 {
				got[m.Addr] = m.Args
				delete(missing, m.Addr)
			}
		}
	}
	return got
}

// drain drops late answers to earlier asks, so a read-back never returns a value from before the write.
func (d *Desk) drain() {
	buf := make([]byte, 2048)
	for {
		_ = d.conn.SetReadDeadline(time.Now().Add(time.Millisecond))
		if _, err := d.conn.Read(buf); err != nil {
			return
		}
	}
}

func (d *Desk) Set(addr string, arg any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.conn.Write(Message{Addr: addr, Args: []any{arg}}.Encode())
	return err
}

// Model asks /xinfo (network address, name, model, firmware: doc p.10, as Thumbmix ConsoleLink reads it).
func (d *Desk) Model() (string, error) {
	r := d.Get("/xinfo")
	if len(r["/xinfo"]) < 3 {
		return "", errSilent
	}
	model, _ := r["/xinfo"][2].(string)
	if !strings.HasPrefix(model, "M32") && !strings.HasPrefix(model, "X32") {
		return "", fmt.Errorf("this is not an M32/X32: it says %q", model)
	}
	return model, nil
}

// value decodes the answer for a param; false when it is missing or has an unexpected type.
func (r Reply) value(p param) (any, bool) {
	args := r[p.addr]
	if len(args) == 0 {
		return nil, false
	}
	return p.decode(args[0])
}

// valueOrNil is value for output: what the desk did not answer is null, never a guess.
func (r Reply) valueOrNil(p param) any {
	v, _ := r.value(p)
	return v
}
