package bridge

import (
	"encoding/base64"
	"net/netip"
	"strings"
	"testing"

	"github.com/mspvantage/smartnodes/vpn/disguise"
)

func line(t *testing.T) Line {
	t.Helper()
	k, err := disguise.NewServerKeys()
	if err != nil {
		t.Fatal(err)
	}
	wg := make([]byte, 32)
	wg[0], wg[31] = 0xfb, 0xff // base64 with '+' and '/' in standard form
	return Line{ChainID: "smartnodes", NodeID: 7, Address: netip.MustParseAddr("203.0.113.9"), Port: 51830,
		WGKey: base64.StdEncoding.EncodeToString(wg), Reality: k.Published(443, "www.example.com")}
}

func TestRoundTrip(t *testing.T) {
	l := line(t)
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
	s := l.String()
	if !strings.HasPrefix(s, "smartnodes-bridge:smartnodes/7/203.0.113.9/51830/") || strings.Contains(s, "+") {
		t.Fatalf("line %s", s)
	}
	back, err := Parse(s)
	if err != nil || back != l {
		t.Fatalf("%+v %v", back, err)
	}
	if back.Endpoint().String() != "203.0.113.9:443" || back.Inner().String() != "127.0.0.1:51830" {
		t.Fatalf("%s %s", back.Endpoint(), back.Inner())
	}
}

func TestFingerprint(t *testing.T) {
	l := line(t)
	fp := l.Fingerprint()
	if len(fp) != 64 || strings.ToLower(fp) != fp {
		t.Fatalf("fingerprint %q", fp)
	}
	// The node id is not part of it: it is known only after registering.
	other := l
	other.NodeID = 99
	if other.Fingerprint() != fp {
		t.Fatal("the node id changed the fingerprint")
	}
	// Everything else is.
	for name, change := range map[string]func(*Line){
		"address":  func(x *Line) { x.Address = netip.MustParseAddr("203.0.113.10") },
		"port":     func(x *Line) { x.Port++ },
		"key":      func(x *Line) { x.WGKey = base64.StdEncoding.EncodeToString(make([]byte, 32)) },
		"disguise": func(x *Line) { x.Reality.ShortID = "00" },
		"chain":    func(x *Line) { x.ChainID = "other" },
	} {
		c := l
		change(&c)
		if c.Fingerprint() == fp {
			t.Errorf("changing the %s kept the fingerprint", name)
		}
	}
}

func TestBadLines(t *testing.T) {
	good := line(t).String()
	for name, s := range map[string]string{
		"no prefix": strings.TrimPrefix(good, "smartnodes-bridge:"),
		"too short": "smartnodes-bridge:smartnodes/7/203.0.113.9",
		"loopback":  strings.Replace(good, "203.0.113.9", "127.0.0.1", 1),
		"bad id":    strings.Replace(good, "/7/", "/x/", 1),
		"bad port":  strings.Replace(good, "/51830/", "/0/", 1),
	} {
		if _, err := Parse(s); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
