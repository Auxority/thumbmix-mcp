package main

import (
	"net"
	"sync"
	"testing"
	"time"
)

// fakeDesk is a tiny UDP desk: it answers a get when it knows the address (like the real desk, it stays
// silent otherwise), stores every set, and records them. It encodes assumptions, not the real desk.
type fakeDesk struct {
	conn   *net.UDPConn
	mu     sync.Mutex
	vals   map[string][]any
	sets   []Message
	silent bool
}

func newFakeDesk(t *testing.T) (*fakeDesk, *Desk) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDesk{conn: conn, vals: map[string][]any{"/xinfo": {"127.0.0.1", "Fake", "M32", "4.06"}}}
	t.Cleanup(func() { _ = conn.Close() })
	go f.serve()
	desk, err := Dial(conn.LocalAddr().String(), 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return f, desk
}

func (f *fakeDesk) serve() {
	buf := make([]byte, 2048)
	for {
		n, from, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		m, err := Decode(buf[:n])
		f.mu.Lock()
		switch {
		case err != nil || f.silent:
		case len(m.Args) > 0:
			f.sets = append(f.sets, m)
			f.vals[m.Addr] = m.Args
		default:
			if args, ok := f.vals[m.Addr]; ok {
				_, _ = f.conn.WriteToUDP(Message{m.Addr, args}.Encode(), from)
			}
		}
		f.mu.Unlock()
	}
}

// seed stores a real value the way the desk would hold it.
func (f *fakeDesk) seed(p param, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vals[p.addr] = []any{p.encode(v)}
}

func (f *fakeDesk) seedName(id, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vals[mustStrip(id).nameAddr()] = []any{name}
}

func (f *fakeDesk) setCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sets)
}

func (f *fakeDesk) setSilent(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.silent = v
}

func (f *fakeDesk) value(p param) (any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	args, ok := f.vals[p.addr]
	if !ok {
		return nil, false
	}
	return p.decode(args[0])
}

// bassDesk has ch/01 "Bass Gtr" with a full EQ band 1 and comp, and ch/02 "Bass DI".
func bassDesk(t *testing.T) (*fakeDesk, *Server) {
	f, desk := newFakeDesk(t)
	s := mustStrip("ch/01")
	f.seedName("ch/01", "Bass Gtr")
	f.seedName("ch/02", "Bass DI")
	f.seedName("ch/03", "Snare")
	f.seed(s.fader(), 0.0)
	f.seed(s.mute(), false)
	f.seed(s.eq(1, "type"), "PEQ")
	f.seed(s.eq(1, "f"), 100.0)
	f.seed(s.eq(1, "g"), 0.0)
	f.seed(s.eq(1, "q"), 2.0)
	for _, field := range []string{"on", "thr", "attack", "release", "mgain", "ratio", "mode"} {
		p := s.dyn(field)
		switch p.kind {
		case pToggle:
			f.seed(p, false)
		case pChoice:
			f.seed(p, p.opts[0])
		default:
			f.seed(p, p.sc.value(0.5))
		}
	}
	return f, &Server{desk: desk}
}
