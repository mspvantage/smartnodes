package main

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// netPlan makes a fresh network space for each probe, so probes never touch
// this machine's own routes, firewall or name servers, and each probe looks
// like a new device. Its only way out is a link to this machine, which
// passes its traffic on to the internet.
type netPlan struct {
	id     uint64
	uplink string
	prefix netip.Prefix // 10.250.<id>.0/24
	mu     sync.Mutex
	slot   int
}

func newNetPlan(id uint64, uplink string) (*netPlan, error) {
	if uplink == "" {
		out, err := exec.Command("ip", "-4", "route", "show", "default").Output()
		if err != nil {
			return nil, err
		}
		f := strings.Fields(string(out))
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "dev" {
				uplink = f[i+1]
				break
			}
		}
		if uplink == "" {
			return nil, fmt.Errorf("no default route; give -uplink")
		}
	}
	pfx := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 250, byte(id % 256), 0}), 24)
	return &netPlan{id: id, uplink: uplink, prefix: pfx}, nil
}

func (n *netPlan) tag() string { return fmt.Sprintf("smartprobe-%d", n.id) }

// hostRules are this machine's rules that pass probe traffic on.
func (n *netPlan) hostRules() [][]string {
	hostIfs := fmt.Sprintf("snp%dh+", n.id)
	c := []string{"-m", "comment", "--comment", n.tag()}
	return [][]string{
		append(append([]string{"-t", "nat", "POSTROUTING", "-s", n.prefix.String(), "-o", n.uplink}, c...), "-j", "MASQUERADE"),
		append(append([]string{"FORWARD", "-i", hostIfs}, c...), "-j", "ACCEPT"),
		append(append([]string{"FORWARD", "-o", hostIfs, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED"}, c...), "-j", "ACCEPT"),
	}
}

func iptables(op string, spec []string) error {
	args := []string{}
	if spec[0] == "-t" {
		args = append(args, spec[0], spec[1])
		spec = spec[2:]
	}
	args = append(args, op, spec[0])
	if op == "-I" {
		args = append(args, "1")
	}
	args = append(args, spec[1:]...)
	return sh("iptables", args...)
}

func sh(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (n *netPlan) setUpHost() error {
	// Clear anything left by an earlier run that did not end cleanly.
	n.tearDownHost()
	if out, err := exec.Command("ip", "netns", "list").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) > 0 && strings.HasPrefix(f[0], fmt.Sprintf("snp%d-", n.id)) {
				n.destroy(f[0])
			}
		}
	}
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0o644); err != nil {
		return fmt.Errorf("turning on forwarding: %w", err)
	}
	for _, r := range n.hostRules() {
		if err := iptables("-I", r); err != nil {
			return err
		}
	}
	return os.MkdirAll("/run/smartprobe", 0o700)
}

func (n *netPlan) tearDownHost() {
	for _, r := range n.hostRules() {
		for iptables("-D", r) == nil {
		}
	}
}

// create makes a network space with a link to this machine.
func (n *netPlan) create() (string, error) {
	n.mu.Lock()
	slot := n.slot
	n.slot = (n.slot + 1) % 64
	n.mu.Unlock()

	ns := fmt.Sprintf("snp%d-%d", n.id, slot)
	hostIf := fmt.Sprintf("snp%dh%d", n.id, slot)
	nsIf := fmt.Sprintf("snp%dn%d", n.id, slot)
	base := n.prefix.Addr().As4()
	base[3] = byte(slot * 4)
	hostAddr := netip.AddrFrom4([4]byte{base[0], base[1], base[2], base[3] + 1})
	nsAddr := netip.AddrFrom4([4]byte{base[0], base[1], base[2], base[3] + 2})

	n.destroy(ns)
	steps := [][]string{
		{"ip", "netns", "add", ns},
		{"ip", "link", "add", hostIf, "type", "veth", "peer", "name", nsIf},
		{"ip", "link", "set", nsIf, "netns", ns},
		{"ip", "address", "add", hostAddr.String() + "/30", "dev", hostIf},
		{"ip", "link", "set", hostIf, "up"},
		{"ip", "-n", ns, "address", "add", nsAddr.String() + "/30", "dev", nsIf},
		{"ip", "-n", ns, "link", "set", nsIf, "up"},
		{"ip", "-n", ns, "link", "set", "lo", "up"},
		{"ip", "-n", ns, "route", "add", "default", "via", hostAddr.String()},
	}
	for _, s := range steps {
		if err := sh(s[0], s[1:]...); err != nil {
			n.destroy(ns)
			return "", err
		}
	}
	// The space's own name server file, which smartvpn rewrites; without it
	// smartvpn would rewrite this machine's.
	dir := "/etc/netns/" + ns
	if err := os.MkdirAll(dir, 0o755); err != nil {
		n.destroy(ns)
		return "", err
	}
	if err := os.WriteFile(dir+"/resolv.conf", []byte("nameserver 1.1.1.1\n"), 0o644); err != nil {
		n.destroy(ns)
		return "", err
	}
	return ns, nil
}

// destroy removes a network space and everything made for it.
func (n *netPlan) destroy(ns string) {
	_ = exec.Command("ip", "netns", "del", ns).Run()
	if i := strings.LastIndexByte(ns, '-'); i > 0 {
		_ = exec.Command("ip", "link", "del", fmt.Sprintf("snp%dh%s", n.id, ns[i+1:])).Run()
	}
	_ = os.RemoveAll("/etc/netns/" + ns)
	_ = os.Remove("/run/smartprobe/" + ns + ".json")
}
