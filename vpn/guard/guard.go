// Package guard sets the firewall rules a SmartNodes node applies to its
// clients' traffic.
//
//   - No node lets clients reach the node machine itself through the tunnel.
//   - An entry node forwards a client's traffic only to registered exit
//     nodes, so it never carries anyone's traffic to the internet.
//   - An exit node forwards to the internet, but never from one client to
//     another.
//
// The rules live in two chains of their own and are replaced in one step
// with iptables-restore, so there is never a moment without them.
package guard

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/mspvantage/smartnodes/vpn/wg"
)

// Role is what a node does with its clients' traffic.
type Role int

const (
	// Entry nodes pass traffic only to exit nodes.
	Entry Role = 1
	// Exit nodes pass traffic to the internet.
	Exit Role = 2
)

// Guard manages the rules for one WireGuard interface.
type Guard struct {
	Iface string
	Run   wg.Runner
}

func (g Guard) chains() (forward, input string) {
	return "SNF-" + g.Iface, "SNI-" + g.Iface
}

func (g Guard) check() error {
	if g.Run == nil || g.Iface == "" || len(g.Iface) > 15 || strings.ContainsAny(g.Iface, " \t\n-") {
		return fmt.Errorf("guard: bad interface %q", g.Iface)
	}
	return nil
}

// Rules returns the iptables-restore input for a role. For an entry node,
// allowed lists the exit nodes' endpoints.
func (g Guard) Rules(role Role, allowed []netip.AddrPort) (string, error) {
	forward, input := g.chains()
	var b strings.Builder
	b.WriteString("*filter\n")
	fmt.Fprintf(&b, ":%s - [0:0]\n:%s - [0:0]\n", forward, input)
	// Nothing reaches the node machine itself through the tunnel.
	fmt.Fprintf(&b, "-A %s -j DROP\n", input)
	switch role {
	case Entry:
		// Sorted, so the same list always gives the same rules.
		sorted := append([]netip.AddrPort(nil), allowed...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Compare(sorted[j]) < 0 })
		for _, ap := range sorted {
			if !ap.IsValid() || !ap.Addr().Is4() {
				return "", fmt.Errorf("guard: endpoint %s is not IPv4", ap)
			}
			for _, proto := range []string{"udp", "tcp"} {
				fmt.Fprintf(&b, "-A %s -d %s/32 -p %s --dport %s -j ACCEPT\n", forward, ap.Addr(), proto, strconv.Itoa(int(ap.Port())))
			}
		}
		fmt.Fprintf(&b, "-A %s -j DROP\n", forward)
	case Exit:
		// Never from one client to another; everything else is left to the
		// machine's own forwarding rules.
		fmt.Fprintf(&b, "-A %s -o %s -j DROP\n", forward, g.Iface)
		fmt.Fprintf(&b, "-A %s -j RETURN\n", forward)
	default:
		return "", fmt.Errorf("guard: unknown role %d", role)
	}
	b.WriteString("COMMIT\n")
	return b.String(), nil
}

// Apply installs or replaces the rules, then makes sure the interface's
// traffic is sent to them.
func (g Guard) Apply(role Role, allowed []netip.AddrPort) error {
	if err := g.check(); err != nil {
		return err
	}
	rules, err := g.Rules(role, allowed)
	if err != nil {
		return err
	}
	if err := g.Run.Run([]byte(rules), "iptables-restore", "--noflush"); err != nil {
		return err
	}
	forward, input := g.chains()
	for _, j := range [][]string{{"INPUT", "-i", g.Iface, "-j", input}, {"FORWARD", "-i", g.Iface, "-j", forward}} {
		// Insert the jump only if it is not there yet, at the top, so no
		// other rule sees this interface's traffic first.
		if err := g.Run.Run(nil, "iptables", append([]string{"-C"}, j...)...); err != nil {
			if err := g.Run.Run(nil, "iptables", append([]string{"-I", j[0], "1"}, j[1:]...)...); err != nil {
				return err
			}
		}
	}
	return nil
}

// Remove takes the rules away.
func (g Guard) Remove() error {
	if err := g.check(); err != nil {
		return err
	}
	forward, input := g.chains()
	_ = g.Run.Run(nil, "iptables", "-D", "INPUT", "-i", g.Iface, "-j", input)
	_ = g.Run.Run(nil, "iptables", "-D", "FORWARD", "-i", g.Iface, "-j", forward)
	for _, c := range []string{input, forward} {
		_ = g.Run.Run(nil, "iptables", "-F", c)
		_ = g.Run.Run(nil, "iptables", "-X", c)
	}
	return nil
}
