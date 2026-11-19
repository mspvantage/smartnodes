package wg

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

type call struct {
	stdin string
	cmd   string
}

type fakeRunner struct {
	calls  []call
	failOn string
}

func (f *fakeRunner) Run(stdin []byte, name string, args ...string) error {
	c := call{stdin: string(stdin), cmd: name + " " + strings.Join(args, " ")}
	f.calls = append(f.calls, c)
	if f.failOn != "" && strings.Contains(c.cmd, f.failOn) {
		return errors.New("failed")
	}
	return nil
}

func (f *fakeRunner) cmds() string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.cmd)
	}
	return strings.Join(out, "\n")
}

var (
	priv    = bytes.Repeat([]byte{1}, 32)
	peerKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
)

func TestSetUpServer(t *testing.T) {
	r := &fakeRunner{}
	i := Interface{Name: "sn0", Run: r}
	if err := i.SetUpServer(priv, 51820, netip.MustParsePrefix("10.77.0.1/24")); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"ip link del sn0",
		"ip link add sn0 type wireguard",
		"wg set sn0 private-key /dev/stdin listen-port 51820",
		"ip address add 10.77.0.1/24 dev sn0",
		"ip link set sn0 up",
	}, "\n")
	if r.cmds() != want {
		t.Fatalf("commands:\n%s", r.cmds())
	}
	// The key travels on standard input, never on the command line.
	if r.calls[2].stdin != base64.StdEncoding.EncodeToString(priv) {
		t.Fatal("private key not passed on stdin")
	}
	if strings.Contains(r.cmds(), base64.StdEncoding.EncodeToString(priv)) {
		t.Fatal("private key on the command line")
	}
}

func TestSetUpFailureCleansUp(t *testing.T) {
	r := &fakeRunner{failOn: "address add"}
	i := Interface{Name: "sn0", Run: r}
	if err := i.SetUpServer(priv, 51820, netip.MustParsePrefix("10.77.0.1/24")); err == nil {
		t.Fatal("expected failure")
	}
	if last := r.calls[len(r.calls)-1].cmd; last != "ip link del sn0" {
		t.Fatalf("interface not removed after failure; last command %q", last)
	}
}

func TestPeers(t *testing.T) {
	r := &fakeRunner{}
	i := Interface{Name: "sn0", Run: r}
	if err := i.AddPeer(peerKey, netip.MustParseAddr("10.77.0.9")); err != nil {
		t.Fatal(err)
	}
	if err := i.RemovePeer(peerKey); err != nil {
		t.Fatal(err)
	}
	want := "wg set sn0 peer " + peerKey + " allowed-ips 10.77.0.9/32\nwg set sn0 peer " + peerKey + " remove"
	if r.cmds() != want {
		t.Fatalf("commands:\n%s", r.cmds())
	}
}

func TestRefusesBadInput(t *testing.T) {
	r := &fakeRunner{}
	good := Interface{Name: "sn0", Run: r}
	bad := []error{
		Interface{Name: "sn0; rm -rf /", Run: r}.Delete(),
		Interface{Name: "", Run: r}.Delete(),
		Interface{Name: "sn0"}.Delete(),
		good.AddPeer("not-a-key", netip.MustParseAddr("10.77.0.9")),
		good.AddPeer("--help", netip.MustParseAddr("10.77.0.9")),
		good.AddPeer(peerKey, netip.MustParseAddr("fd00::1")),
		good.RemovePeer("x"),
		good.SetUpServer(priv[:31], 1, netip.MustParsePrefix("10.77.0.1/24")),
		good.SetUpClient(priv, "x", netip.MustParseAddrPort("10.0.0.1:1"), netip.MustParseAddr("10.77.0.2")),
		good.SetUpClient(priv, peerKey, netip.AddrPort{}, netip.MustParseAddr("10.77.0.2")),
	}
	for n, err := range bad {
		if err == nil {
			t.Fatalf("case %d accepted", n)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("commands ran for bad input:\n%s", r.cmds())
	}
}

func TestSetUpClient(t *testing.T) {
	r := &fakeRunner{}
	i := Interface{Name: "sv0", Run: r}
	err := i.SetUpClient(priv, peerKey, netip.MustParseAddrPort("10.200.0.1:51820"), netip.MustParseAddr("10.77.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"ip link del sv0",
		"ip link add sv0 type wireguard",
		"wg set sv0 private-key /dev/stdin peer " + peerKey + " endpoint 10.200.0.1:51820 allowed-ips 0.0.0.0/0 persistent-keepalive 25",
		"ip address add 10.77.0.2/32 dev sv0",
		"ip link set sv0 up",
		"ip route replace default dev sv0",
	}, "\n")
	if r.cmds() != want {
		t.Fatalf("commands:\n%s", r.cmds())
	}
}

func TestSetUpTunnelForTwoHops(t *testing.T) {
	r := &fakeRunner{}
	outer := Interface{Name: "sv0", Run: r}
	err := outer.SetUpTunnel(Tunnel{
		PrivateKey: priv, NodeKey: peerKey,
		Endpoint: netip.MustParseAddrPort("10.200.0.1:51820"), Address: netip.MustParseAddr("10.77.0.2"),
		Allowed: []netip.Prefix{netip.MustParsePrefix("10.201.0.2/32")},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"ip link del sv0",
		"ip link add sv0 type wireguard",
		"wg set sv0 private-key /dev/stdin peer " + peerKey + " endpoint 10.200.0.1:51820 allowed-ips 10.201.0.2/32 persistent-keepalive 25",
		"ip address add 10.77.0.2/32 dev sv0",
		"ip link set sv0 up",
		"ip route replace 10.201.0.2/32 dev sv0",
	}, "\n")
	if r.cmds() != want {
		t.Fatalf("outer:\n%s", r.cmds())
	}

	r2 := &fakeRunner{}
	inner := Interface{Name: "sv1", Run: r2}
	err = inner.SetUpTunnel(Tunnel{
		PrivateKey: priv, NodeKey: peerKey,
		Endpoint: netip.MustParseAddrPort("10.201.0.2:51821"), Address: netip.MustParseAddr("10.78.0.2"),
		Allowed: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, MTU: 1340,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r2.cmds(), "ip link set sv1 mtu 1340\nip link set sv1 up\nip route replace default dev sv1") {
		t.Fatalf("inner:\n%s", r2.cmds())
	}

	for name, bad := range map[string]Tunnel{
		"no allowed": {PrivateKey: priv, NodeKey: peerKey, Endpoint: netip.MustParseAddrPort("10.0.0.1:1"), Address: netip.MustParseAddr("10.77.0.2")},
		"ipv6 allowed": {PrivateKey: priv, NodeKey: peerKey, Endpoint: netip.MustParseAddrPort("10.0.0.1:1"), Address: netip.MustParseAddr("10.77.0.2"),
			Allowed: []netip.Prefix{netip.MustParsePrefix("::/0")}},
		"tiny mtu": {PrivateKey: priv, NodeKey: peerKey, Endpoint: netip.MustParseAddrPort("10.0.0.1:1"), Address: netip.MustParseAddr("10.77.0.2"),
			Allowed: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, MTU: 500},
	} {
		r3 := &fakeRunner{}
		if err := (Interface{Name: "sv2", Run: r3}).SetUpTunnel(bad); err == nil || len(r3.calls) != 0 {
			t.Fatalf("%s accepted", name)
		}
	}
}
