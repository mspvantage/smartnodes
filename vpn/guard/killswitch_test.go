package guard

import (
	"net/netip"
	"strings"
	"testing"
)

func TestKillSwitchRules(t *testing.T) {
	k := KillSwitch{Tunnels: []string{"sv0", "sv1"}}
	v4, v6, err := k.Rules(netip.MustParseAddrPort("203.0.113.5:51820"))
	if err != nil {
		t.Fatal(err)
	}
	want := `*filter
:SNV-OUT - [0:0]
-A SNV-OUT -o lo -j ACCEPT
-A SNV-OUT -o sv0 -j ACCEPT
-A SNV-OUT -o sv1 -j ACCEPT
-A SNV-OUT -d 203.0.113.5/32 -p udp --dport 51820 -j ACCEPT
-A SNV-OUT -d 203.0.113.5/32 -p tcp --dport 51820 -j ACCEPT
-A SNV-OUT -p udp --sport 68 --dport 67 -j ACCEPT
-A SNV-OUT -j REJECT
COMMIT
`
	if v4 != want {
		t.Fatalf("v4:\n%s", v4)
	}
	if !strings.Contains(v6, "-A SNV-OUT -o lo -j ACCEPT\n-A SNV-OUT -j REJECT\n") || strings.Contains(v6, "sv") {
		t.Fatalf("v6:\n%s", v6)
	}
	if _, _, err := (KillSwitch{Tunnels: []string{"a b"}}).Rules(netip.MustParseAddrPort("1.2.3.4:1")); err == nil {
		t.Fatal("bad interface accepted")
	}
	if _, _, err := k.Rules(netip.MustParseAddrPort("[fd00::1]:1")); err == nil {
		t.Fatal("IPv6 entry accepted")
	}
}

func TestKillSwitchApplyRemove(t *testing.T) {
	r := &fakeRunner{}
	k := KillSwitch{Run: r, Tunnels: []string{"sv0"}}
	if err := k.Apply(netip.MustParseAddrPort("1.2.3.4:5")); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(r.calls, "\n")
	for _, c := range []string{"iptables-restore --noflush", "iptables -I OUTPUT 1 -j SNV-OUT", "ip6tables-restore --noflush", "ip6tables -I OUTPUT 1 -j SNV-OUT"} {
		if !strings.Contains(got, c) {
			t.Fatalf("missing %q:\n%s", c, got)
		}
	}
	r2 := &fakeRunner{}
	(KillSwitch{Run: r2}).Remove()
	if got := strings.Join(r2.calls, "\n"); !strings.Contains(got, "ip6tables -X SNV-OUT") || !strings.Contains(got, "iptables -D OUTPUT -j SNV-OUT") {
		t.Fatalf("remove:\n%s", got)
	}
}
