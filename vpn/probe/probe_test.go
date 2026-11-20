package probe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSealMatchesTheChain(t *testing.T) {
	// Computed independently, in Python, from the chain's definition.
	salt := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	got := Seal("smartnodes", 7, 4, 2, 1, 37, salt)
	if got != "6663525ef43751c87556f38eefd6f41606029a3d897b293c3e3241b881acc008" {
		t.Fatalf("seal %s", got)
	}
}

func TestVerdict(t *testing.T) {
	good := Measurement{Reached: BothHops, Mbps: 50, IntegrityOK: true, DNSOK: true, Checked: true}
	cases := []struct {
		name   string
		role   Role
		m      Measurement
		result uint64
		band   uint64
		ok     bool
	}{
		{"entry pass", Entry, good, ResultPass, 50, true},
		{"exit pass", Exit, good, ResultPass, 50, true},
		{"entry unreachable", Entry, Measurement{Reached: NoHop}, ResultFail, 0, true},
		{"exit judged, entry partner down", Exit, Measurement{Reached: NoHop}, 0, 0, false},
		{"exit unreachable through a working entry", Exit, Measurement{Reached: EntryOnly}, ResultFail, 0, true},
		{"entry judged, exit partner down", Entry, Measurement{Reached: EntryOnly}, 0, 0, false},
		{"exit too slow", Exit, Measurement{Reached: BothHops, Mbps: 2, IntegrityOK: true, DNSOK: true, Checked: true}, ResultFail, 2, true},
		{"exit alters content", Exit, Measurement{Reached: BothHops, Mbps: 50, IntegrityOK: false, DNSOK: true, Checked: true}, ResultTamper, 50, true},
		{"exit lies about names", Exit, Measurement{Reached: BothHops, Mbps: 50, IntegrityOK: true, DNSOK: false, Checked: true}, ResultTamper, 50, true},
		{"entry not blamed for tampering", Entry, Measurement{Reached: BothHops, Mbps: 50, IntegrityOK: false, DNSOK: true, Checked: true}, 0, 0, false},
		{"exit passes nothing at all", Exit, Measurement{Reached: BothHops}, ResultFail, 0, true},
		{"entry, nothing through the exit", Entry, Measurement{Reached: BothHops}, 0, 0, false},
	}
	for _, c := range cases {
		r, b, ok := Verdict(c.role, c.m, 10)
		if r != c.result || b != c.band || ok != c.ok {
			t.Errorf("%s: got %d %d %v, want %d %d %v", c.name, r, b, ok, c.result, c.band, c.ok)
		}
	}
	if SpeedBand(1000) != 255 || SpeedBand(-1) != 0 || SpeedBand(12.6) != 13 {
		t.Fatal("speed band")
	}
}

type fakeResolver map[string][]string

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	a, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	var out []netip.Addr
	for _, s := range a {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

func TestCheckDNS(t *testing.T) {
	honest := fakeResolver{"dns.google": {"8.8.4.4"}, "one.one.one.one": {"1.1.1.1", "1.0.0.1"}}
	if ok, n := CheckDNS(context.Background(), honest, Fixed); !ok || n != 2 {
		t.Fatalf("honest %v %d", ok, n)
	}
	liar := fakeResolver{"dns.google": {"203.0.113.9"}, "one.one.one.one": {"1.1.1.1"}}
	if ok, _ := CheckDNS(context.Background(), liar, Fixed); ok {
		t.Fatal("a lie passed")
	}
	silent := fakeResolver{}
	if ok, n := CheckDNS(context.Background(), silent, Fixed); !ok || n != 0 {
		t.Fatal("no answer counted as a lie")
	}
}

func TestMeasureThrough(t *testing.T) {
	body := strings.Repeat("x", 1<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file":
			fmt.Fprint(w, "known content")
		case "/altered":
			fmt.Fprint(w, "known content, altered")
		default:
			fmt.Fprint(w, body)
		}
	}))
	defer srv.Close()
	sum, _, err := Fetch(context.Background(), srv.Client(), srv.URL+"/file", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	tools := Tools{Client: srv.Client(), Resolver: fakeResolver{"dns.google": {"8.8.8.8"}}}

	m := MeasureThrough(context.Background(), tools, srv.URL+"/speed", 5*time.Second, []Known{{URL: srv.URL + "/file", SHA256: sum}}, Fixed)
	if !m.Checked || !m.IntegrityOK || !m.DNSOK || m.Mbps <= 0 {
		t.Fatalf("%+v", m)
	}
	m = MeasureThrough(context.Background(), tools, srv.URL+"/speed", 5*time.Second, []Known{{URL: srv.URL + "/altered", SHA256: sum}}, Fixed)
	if m.IntegrityOK {
		t.Fatal("altered content passed")
	}
}

func TestStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	salt, _ := NewSalt()
	if len(salt) != 64 {
		t.Fatalf("salt %q", salt)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Add(Report{Round: 5, NodeID: 1, Result: 1, Salt: salt}, Report{Round: 5, NodeID: 0, Result: 2, Salt: salt}))
	if got := s.Uncommitted(5); len(got) != 2 || got[0].NodeID != 0 {
		t.Fatalf("uncommitted %+v", got)
	}
	must(s.MarkCommitted(5))
	must(s.Add(Report{Round: 4, NodeID: 9, Result: 1, Salt: salt, Committed: true})) // too old
	must(s.Add(Report{Round: 6, NodeID: 3, Result: 1, Salt: salt}))                  // this round's
	// After a restart, round 6 opens round 5's reports.
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	open, err := s2.ToOpen(6)
	if err != nil || len(open) != 2 || open[0].NodeID != 0 || open[1].NodeID != 1 {
		t.Fatalf("to open %+v %v", open, err)
	}
	must(s2.Remove(5, 0))
	must(s2.Remove(5, 1))
	if left := s2.Uncommitted(6); len(left) != 1 {
		t.Fatalf("current round lost: %+v", left)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
}
