package main

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseRouteGet(t *testing.T) {
	for in, want := range map[string][2]string{
		"1.1.1.1 via 192.168.1.1 dev eth0 src 192.168.1.5 uid 0 \\    cache": {"192.168.1.1", "eth0"},
		"10.200.0.1 dev sn-c src 10.200.0.2 uid 0":                           {"", "sn-c"},
		"10.201.0.2 dev sv0 src 10.77.0.2 uid 0":                             {"", "sv0"},
	} {
		via, dev, err := parseRouteGet(in)
		if err != nil || via != want[0] || dev != want[1] {
			t.Fatalf("%q: %q %q %v", in, via, dev, err)
		}
	}
	if _, _, err := parseRouteGet("garbage"); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, _, err := parseRouteGet("1.1.1.1 via x;rm dev eth0"); err == nil {
		t.Fatal("bad gateway accepted")
	}
}

func fakeCommands(t *testing.T, routes map[string]string) *[]string {
	var ran []string
	stateFile = filepath.Join(t.TempDir(), "run", "state.json")
	output = func(name string, args ...string) (string, error) {
		return routes[strings.Join(args, " ")], nil
	}
	run = func(name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}
	return &ran
}

func TestRoutesRoundTrip(t *testing.T) {
	ran := fakeCommands(t, map[string]string{
		"-4 route show default":       "default via 192.168.1.1 dev eth0 proto dhcp metric 100\n",
		"-4 -o route get 203.0.113.5": "203.0.113.5 via 192.168.1.1 dev eth0 src 192.168.1.5",
	})
	entry := netip.MustParseAddr("203.0.113.5")
	if err := prepareRoutes(entry); err != nil {
		t.Fatal(err)
	}
	if err := prepareRoutes(entry); err == nil {
		t.Fatal("connected twice")
	}
	if fi, err := os.Stat(stateFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file %v %v", fi, err)
	}
	if err := restoreRoutes(); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"ip route replace 203.0.113.5/32 via 192.168.1.1 dev eth0",
		"ip route replace default via 192.168.1.1 dev eth0 proto dhcp metric 100",
		"ip route del 203.0.113.5/32 via 192.168.1.1 dev eth0",
	}, "\n")
	if got := strings.Join(*ran, "\n"); got != want {
		t.Fatalf("ran:\n%s", got)
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatal("state file left behind")
	}
	// Down with nothing to undo is fine.
	if err := restoreRoutes(); err != nil {
		t.Fatal(err)
	}
}

func TestRefuseEntryThroughTunnel(t *testing.T) {
	fakeCommands(t, map[string]string{
		"-4 -o route get 203.0.113.5": "203.0.113.5 dev sv1 src 10.78.0.2",
	})
	if err := prepareRoutes(netip.MustParseAddr("203.0.113.5")); err == nil {
		t.Fatal("pinned the entry through an old tunnel")
	}
}

func TestCreditFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credit.json")
	now := time.Unix(1_800_000_000, 0)
	c := creditFile{ChainID: "smartnodes", ExitID: 1, ExitKey: "k", Endpoint: "10.0.0.1:1", WGPrivate: "p", Expires: now.Add(time.Hour).Unix()}
	if err := saveCredit(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := loadCredit(path, now)
	if err != nil || got != c {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := loadCredit(path, now.Add(time.Hour)); err == nil {
		t.Fatal("lapsed credit loaded")
	}
	_ = os.Chmod(path, 0o644)
	if _, err := loadCredit(path, now); err == nil {
		t.Fatal("world-readable credit loaded")
	}
}

func TestRandomWait(t *testing.T) {
	for i := 0; i < 200; i++ {
		d := randomWait(10 * time.Minute)
		if d < 2*time.Minute || d > 10*time.Minute {
			t.Fatalf("wait %s", d)
		}
	}
	if randomWait(0) != 0 {
		t.Fatal("zero wait")
	}
}

func TestResolverRoundTrip(t *testing.T) {
	fakeCommands(t, map[string]string{
		"-4 route show default":       "default via 192.168.1.1 dev eth0\n",
		"-4 -o route get 203.0.113.5": "203.0.113.5 via 192.168.1.1 dev eth0",
	})
	resolvPath = filepath.Join(t.TempDir(), "resolv.conf")
	_ = os.WriteFile(resolvPath, []byte("nameserver 192.168.1.1\n"), 0o644)
	if err := prepareRoutes(netip.MustParseAddr("203.0.113.5")); err != nil {
		t.Fatal(err)
	}
	if err := setResolver("8.8.8.8; rm"); err == nil {
		t.Fatal("bad name server accepted")
	}
	if err := setResolver("10.78.0.1"); err != nil {
		t.Fatal(err)
	}
	// A second session keeps the original, not the tunnel's.
	if err := setResolver("10.78.0.1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(resolvPath); !strings.Contains(string(got), "nameserver 10.78.0.1\n") {
		t.Fatalf("resolv.conf %q", got)
	}
	if err := restoreRoutes(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(resolvPath); string(got) != "nameserver 192.168.1.1\n" {
		t.Fatalf("not restored: %q", got)
	}
}

func TestBlockedEntryDetection(t *testing.T) {
	if !unreachable(errors.New("cannot reach node 3: i/o timeout"), 3) || !unreachable(errors.New("node 3 did not answer properly: x"), 3) {
		t.Fatal("unreachable entry not recognised")
	}
	if unreachable(errors.New("node 3 refused: this node is full"), 3) || unreachable(errors.New("cannot reach node 31: x"), 3) {
		t.Fatal("a refusal or another node counted as blocked")
	}
	output = func(string, ...string) (string, error) { return "abc=\t0\n", nil }
	if handshaken("sv0") {
		t.Fatal("no handshake counted as one")
	}
	output = func(string, ...string) (string, error) { return "abc=\t1763600000\n", nil }
	if !handshaken("sv0") {
		t.Fatal("handshake missed")
	}
}

func TestBlockedMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transport.json")
	now := time.Unix(1_800_000_000, 0)
	if blockedHere(path, now) || blockedHere("", now) {
		t.Fatal("blocked with no memory")
	}
	rememberBlocked(path, now)
	if !blockedHere(path, now.Add(time.Hour)) {
		t.Fatal("forgot within the day")
	}
	if blockedHere(path, now.Add(25*time.Hour)) {
		t.Fatal("remembered for too long")
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "node") || strings.Contains(string(raw), ".") {
		t.Fatalf("the memory holds more than a time: %s", raw)
	}
	forgetBlocked(path)
	if blockedHere(path, now) {
		t.Fatal("not forgotten")
	}
}

func TestReadBridge(t *testing.T) {
	if got, err := readBridge("smartnodes-bridge:x"); err != nil || got != "smartnodes-bridge:x" {
		t.Fatalf("%q %v", got, err)
	}
	path := filepath.Join(t.TempDir(), "bridges.txt")
	if err := appendLine(path, "line-a"); err != nil {
		t.Fatal(err)
	}
	_ = appendLine(path, "line-b")
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		got, err := readBridge("@" + path)
		if err != nil {
			t.Fatal(err)
		}
		seen[got] = true
	}
	if !seen["line-a"] || !seen["line-b"] || len(seen) != 2 {
		t.Fatalf("picked %v", seen)
	}
	empty := filepath.Join(t.TempDir(), "none.txt")
	_ = os.WriteFile(empty, []byte("# nothing\n"), 0o600)
	if _, err := readBridge("@" + empty); err == nil {
		t.Fatal("an empty file gave a line")
	}
}
