package guard

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls []string
	stdin []string
	// exists answers "iptables -C" checks.
	exists bool
}

func (f *fakeRunner) Run(stdin []byte, name string, args ...string) error {
	c := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, c)
	f.stdin = append(f.stdin, string(stdin))
	if len(args) > 0 && args[0] == "-C" && !f.exists {
		return errors.New("no such rule")
	}
	return nil
}

func TestEntryRules(t *testing.T) {
	g := Guard{Iface: "sn0"}
	rules, err := g.Rules(Entry, []netip.AddrPort{
		netip.MustParseAddrPort("10.201.0.2:51821"),
		netip.MustParseAddrPort("10.1.1.1:443"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `*filter
:SNF-sn0 - [0:0]
:SNI-sn0 - [0:0]
-A SNI-sn0 -j DROP
-A SNF-sn0 -d 10.1.1.1/32 -p udp --dport 443 -j ACCEPT
-A SNF-sn0 -d 10.1.1.1/32 -p tcp --dport 443 -j ACCEPT
-A SNF-sn0 -d 10.201.0.2/32 -p udp --dport 51821 -j ACCEPT
-A SNF-sn0 -d 10.201.0.2/32 -p tcp --dport 51821 -j ACCEPT
-A SNF-sn0 -j DROP
COMMIT
`
	if rules != want {
		t.Fatalf("rules:\n%s", rules)
	}
	// With no exits known, an entry passes nothing on.
	rules, _ = g.Rules(Entry, nil)
	if !strings.Contains(rules, "-A SNF-sn0 -j DROP") || strings.Contains(rules, "ACCEPT") {
		t.Fatalf("empty entry rules:\n%s", rules)
	}
}

func TestExitRules(t *testing.T) {
	rules, err := Guard{Iface: "sx0"}.Rules(Exit, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"-A SNI-sx0 -j DROP", "-A SNF-sx0 -o sx0 -j DROP", "-A SNF-sx0 -j RETURN"} {
		if !strings.Contains(rules, line+"\n") {
			t.Fatalf("missing %q in:\n%s", line, rules)
		}
	}
}

func TestBadInput(t *testing.T) {
	if _, err := (Guard{Iface: "sn0"}).Rules(Entry, []netip.AddrPort{netip.MustParseAddrPort("[fd00::1]:1")}); err == nil {
		t.Fatal("IPv6 endpoint accepted")
	}
	if _, err := (Guard{Iface: "sn0"}).Rules(Role(9), nil); err == nil {
		t.Fatal("unknown role accepted")
	}
	r := &fakeRunner{}
	for _, name := range []string{"", "a b", "-j", "waytoolonginterface"} {
		if err := (Guard{Iface: name, Run: r}).Apply(Exit, nil); err == nil {
			t.Fatalf("interface %q accepted", name)
		}
	}
	if len(r.calls) != 0 {
		t.Fatal("commands ran for bad input")
	}
}

func TestApplyInstallsJumpsOnce(t *testing.T) {
	r := &fakeRunner{}
	g := Guard{Iface: "sn0", Run: r}
	if err := g.Apply(Entry, nil); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"iptables-restore --noflush",
		"iptables -C INPUT -i sn0 -j SNI-sn0",
		"iptables -I INPUT 1 -i sn0 -j SNI-sn0",
		"iptables -C FORWARD -i sn0 -j SNF-sn0",
		"iptables -I FORWARD 1 -i sn0 -j SNF-sn0",
	}, "\n")
	if got := strings.Join(r.calls, "\n"); got != want {
		t.Fatalf("calls:\n%s", got)
	}
	if !strings.HasPrefix(r.stdin[0], "*filter") {
		t.Fatal("rules not passed on stdin")
	}
	// Applying again, as when the list of exits changes, replaces the rules
	// and adds no second jump.
	r2 := &fakeRunner{exists: true}
	if err := (Guard{Iface: "sn0", Run: r2}).Apply(Entry, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range r2.calls {
		if strings.Contains(c, " -I ") {
			t.Fatalf("jump inserted twice: %s", c)
		}
	}
}

func TestRemove(t *testing.T) {
	r := &fakeRunner{}
	if err := (Guard{Iface: "sn0", Run: r}).Remove(); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(r.calls, "\n")
	for _, c := range []string{"iptables -D INPUT -i sn0 -j SNI-sn0", "iptables -D FORWARD -i sn0 -j SNF-sn0", "iptables -X SNF-sn0", "iptables -X SNI-sn0"} {
		if !strings.Contains(got, c) {
			t.Fatalf("missing %q in:\n%s", c, got)
		}
	}
}

func TestExitRulesWithDNS(t *testing.T) {
	rules, err := Guard{Iface: "sx0", DNS: netip.MustParseAddr("10.78.0.1")}.Rules(Exit, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "-A SNI-sx0 -d 10.78.0.1/32 -p udp --dport 53 -j ACCEPT\n-A SNI-sx0 -d 10.78.0.1/32 -p tcp --dport 53 -j ACCEPT\n-A SNI-sx0 -j DROP\n"
	if !strings.Contains(rules, want) {
		t.Fatalf("rules:\n%s", rules)
	}
	// An entry node offers no name server.
	rules, _ = Guard{Iface: "sn0", DNS: netip.MustParseAddr("10.77.0.1")}.Rules(Entry, nil)
	if strings.Contains(rules, "--dport 53") {
		t.Fatalf("entry rules:\n%s", rules)
	}
}

func TestBridgeLock(t *testing.T) {
	r := &fakeRunner{}
	if err := (BridgeLock{Iface: "sb0", Port: 51830, Run: r}).Apply(); err != nil {
		t.Fatal(err)
	}
	want := "*filter\n:SNB-sb0 - [0:0]\n-A SNB-sb0 -i lo -j RETURN\n-A SNB-sb0 -p udp --dport 51830 -j DROP\n-A SNB-sb0 -p tcp --dport 51830 -j DROP\nCOMMIT\n"
	if r.stdin[0] != want {
		t.Fatalf("rules:\n%s", r.stdin[0])
	}
	if got := strings.Join(r.calls, "\n"); !strings.Contains(got, "iptables -I INPUT 1 -j SNB-sb0") {
		t.Fatalf("calls:\n%s", got)
	}
	if err := (BridgeLock{Iface: "sb0", Run: r}).Apply(); err == nil {
		t.Fatal("no port accepted")
	}
}
