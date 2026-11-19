package guard

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/mspvantage/smartnodes/vpn/wg"
)

// KillSwitch keeps a client's traffic inside its tunnels. While it is on,
// the machine sends IPv4 only through the tunnel interfaces, to the entry
// node itself and to its local DHCP server, and sends no IPv6 at all. It
// stays on if a tunnel drops or the client program dies, until the user
// disconnects, so nothing ever leaves unprotected.
type KillSwitch struct {
	Run wg.Runner
	// Tunnels are the client's WireGuard interfaces.
	Tunnels []string
}

const killChain = "SNV-OUT"

// Rules returns the iptables-restore input for IPv4 and for IPv6.
func (k KillSwitch) Rules(entry netip.AddrPort) (v4, v6 string, err error) {
	if !entry.IsValid() || !entry.Addr().Is4() {
		return "", "", fmt.Errorf("killswitch: entry %s is not IPv4", entry)
	}
	if len(k.Tunnels) == 0 {
		return "", "", fmt.Errorf("killswitch: no tunnel interfaces")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "*filter\n:%s - [0:0]\n", killChain)
	fmt.Fprintf(&b, "-A %s -o lo -j ACCEPT\n", killChain)
	for _, t := range k.Tunnels {
		if t == "" || len(t) > 15 || strings.ContainsAny(t, " \t\n-") {
			return "", "", fmt.Errorf("killswitch: bad interface %q", t)
		}
		fmt.Fprintf(&b, "-A %s -o %s -j ACCEPT\n", killChain, t)
	}
	for _, proto := range []string{"udp", "tcp"} {
		fmt.Fprintf(&b, "-A %s -d %s/32 -p %s --dport %d -j ACCEPT\n", killChain, entry.Addr(), proto, entry.Port())
	}
	// Keeps the machine's own network address leased.
	fmt.Fprintf(&b, "-A %s -p udp --sport 68 --dport 67 -j ACCEPT\n", killChain)
	fmt.Fprintf(&b, "-A %s -j REJECT\nCOMMIT\n", killChain)
	v4 = b.String()
	v6 = fmt.Sprintf("*filter\n:%s - [0:0]\n-A %s -o lo -j ACCEPT\n-A %s -j REJECT\nCOMMIT\n", killChain, killChain, killChain)
	return v4, v6, nil
}

// Apply turns the switch on, or updates it for a new entry node.
func (k KillSwitch) Apply(entry netip.AddrPort) error {
	v4, v6, err := k.Rules(entry)
	if err != nil {
		return err
	}
	for _, fam := range []struct{ tool, rules string }{{"iptables", v4}, {"ip6tables", v6}} {
		if err := k.Run.Run([]byte(fam.rules), fam.tool+"-restore", "--noflush"); err != nil {
			return err
		}
		if err := k.Run.Run(nil, fam.tool, "-C", "OUTPUT", "-j", killChain); err != nil {
			if err := k.Run.Run(nil, fam.tool, "-I", "OUTPUT", "1", "-j", killChain); err != nil {
				return err
			}
		}
	}
	return nil
}

// Remove turns the switch off.
func (k KillSwitch) Remove() {
	for _, tool := range []string{"iptables", "ip6tables"} {
		_ = k.Run.Run(nil, tool, "-D", "OUTPUT", "-j", killChain)
		_ = k.Run.Run(nil, tool, "-F", killChain)
		_ = k.Run.Run(nil, tool, "-X", killChain)
	}
}
