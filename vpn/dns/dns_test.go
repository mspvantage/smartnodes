package dns

import (
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// fakeUpstream answers every query with the query plus "!".
func fakeUpstream(t *testing.T) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close(); ln.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			// A stray answer with the wrong id first, which must be ignored.
			_, _ = pc.WriteTo(append([]byte{0xff, 0xff}, make([]byte, 10)...), from)
			_, _ = pc.WriteTo(append(append([]byte(nil), buf[:n]...), '!'), from)
		}
	}()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var l [2]byte
				if _, err := io.ReadFull(c, l[:]); err != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(l[:]))
				if _, err := io.ReadFull(c, q); err != nil {
					return
				}
				a := append(q, '!')
				binary.BigEndian.PutUint16(l[:], uint16(len(a)))
				_, _ = c.Write(append(l[:], a...))
			}()
		}
	}()
	return netip.MustParseAddrPort(pc.LocalAddr().String())
}

func TestForwards(t *testing.T) {
	up := fakeUpstream(t)
	// Port 53 needs root; test the parts on a free port instead.
	f := Forwarder{Upstream: up}
	query := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 'q'}
	answer, err := f.ask(query)
	if err != nil || string(answer) != string(query)+"!" {
		t.Fatalf("%q %v", answer, err)
	}
	if _, err := f.ask([]byte{1, 2}); err == nil {
		t.Fatal("short query forwarded")
	}

	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer pc.Close()
	defer ln.Close()
	slots := make(chan struct{}, 4)
	go f.serveUDP(pc, slots)
	go f.serveTCP(ln, slots)

	c, _ := net.Dial("udp", pc.LocalAddr().String())
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = c.Write(query)
	buf := make([]byte, 512)
	n, err := c.Read(buf)
	if err != nil || string(buf[:n]) != string(query)+"!" {
		t.Fatalf("udp %q %v", buf[:n], err)
	}

	tc, _ := net.Dial("tcp", ln.Addr().String())
	defer tc.Close()
	_ = tc.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = tc.Write(append([]byte{0, byte(len(query))}, query...))
	var l [2]byte
	if _, err := io.ReadFull(tc, l[:]); err != nil {
		t.Fatal(err)
	}
	a := make([]byte, l[1])
	if _, err := io.ReadFull(tc, a); err != nil || string(a) != string(query)+"!" {
		t.Fatalf("tcp %q %v", a, err)
	}
}

func TestUpstream(t *testing.T) {
	got, err := Upstream("# comment\nsearch lan\nnameserver 127.0.0.53\nnameserver 1.1.1.1\n")
	if err != nil || got.String() != "127.0.0.53:53" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := Upstream("search lan\n"); err == nil {
		t.Fatal("no name server accepted")
	}
}

func question(name string, qtype uint16) []byte {
	q := []byte{0xab, 0xcd, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 1}
	for _, l := range strings.Split(name, ".") {
		q = append(q, byte(len(l)))
		q = append(q, l...)
	}
	q = append(q, 0, byte(qtype>>8), byte(qtype), 0, 1)
	// An EDNS record in the additional section, as resolvers send.
	q = append(q, 0, 0, 41, 0x10, 0, 0, 0, 0, 0, 0, 0)
	return q
}

func TestNoIPv6(t *testing.T) {
	r, ok := NoIPv6(question("www.wikipedia.org", 28))
	if !ok {
		t.Fatal("AAAA not answered")
	}
	if r[0] != 0xab || r[1] != 0xcd || r[2]&0x80 == 0 || r[2]&0x01 == 0 || r[3]&0x0f != 0 {
		t.Fatalf("header % x", r[:4])
	}
	if r[5] != 1 || r[7] != 0 || r[9] != 0 || r[11] != 0 {
		t.Fatalf("counts % x", r[4:12])
	}
	if len(r) != 12+len("www.wikipedia.org")+2+4 {
		t.Fatalf("length %d", len(r))
	}
	if _, ok := NoIPv6(question("www.wikipedia.org", 1)); ok {
		t.Fatal("an A query was answered empty")
	}
	if _, ok := NoIPv6([]byte{1, 2, 3}); ok {
		t.Fatal("garbage answered")
	}
	bad := question("a.b", 28)
	bad[12] = 0xc0 // a compression pointer in a question
	if _, ok := NoIPv6(bad); ok {
		t.Fatal("compressed question answered")
	}
}
