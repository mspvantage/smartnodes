// Package wg sets up WireGuard interfaces by running the standard "ip" and
// "wg" commands. It uses the WireGuard built into the Linux kernel.
//
// Keys and addresses are checked before they are passed on, and commands are
// run directly, never through a shell.
package wg

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Runner runs one command. Tests replace it.
type Runner interface {
	Run(stdin []byte, name string, args ...string) error
}

// Exec runs commands for real.
type Exec struct{}

// Run runs a command, feeding stdin to it, and returns its output on failure.
func (Exec) Run(stdin []byte, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

var ifaceName = regexp.MustCompile(`^[a-z][a-z0-9]{0,14}$`)

// Interface is one WireGuard interface.
type Interface struct {
	Name string
	Run  Runner
}

func (i Interface) check() error {
	if !ifaceName.MatchString(i.Name) {
		return fmt.Errorf("wg: bad interface name %q", i.Name)
	}
	if i.Run == nil {
		return fmt.Errorf("wg: no command runner")
	}
	return nil
}

// checkKey accepts only a 32-byte key in standard base64.
func checkKey(key string) error {
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 {
		return fmt.Errorf("wg: bad key")
	}
	return nil
}

// Delete removes the interface if it exists. Removing it also forgets every
// peer, which is how a node forgets all sessions at once.
func (i Interface) Delete() error {
	if err := i.check(); err != nil {
		return err
	}
	_ = i.Run.Run(nil, "ip", "link", "del", i.Name)
	return nil
}

// SetUpServer creates the node's interface, replacing any old one. The
// private key is passed on standard input, never written to a file.
func (i Interface) SetUpServer(privateKey []byte, port uint16, address netip.Prefix) error {
	if err := i.check(); err != nil {
		return err
	}
	if len(privateKey) != 32 {
		return fmt.Errorf("wg: private key must be 32 bytes")
	}
	_ = i.Delete()
	key := []byte(base64.StdEncoding.EncodeToString(privateKey))
	steps := []struct {
		stdin []byte
		args  []string
	}{
		{nil, []string{"ip", "link", "add", i.Name, "type", "wireguard"}},
		{key, []string{"wg", "set", i.Name, "private-key", "/dev/stdin", "listen-port", strconv.Itoa(int(port))}},
		{nil, []string{"ip", "address", "add", address.String(), "dev", i.Name}},
		{nil, []string{"ip", "link", "set", i.Name, "up"}},
	}
	for _, s := range steps {
		if err := i.Run.Run(s.stdin, s.args[0], s.args[1:]...); err != nil {
			_ = i.Delete()
			return err
		}
	}
	return nil
}

// AddPeer lets a session's key use one tunnel address.
func (i Interface) AddPeer(publicKey string, addr netip.Addr) error {
	if err := i.check(); err != nil {
		return err
	}
	if err := checkKey(publicKey); err != nil {
		return err
	}
	if !addr.Is4() {
		return fmt.Errorf("wg: tunnel address must be IPv4")
	}
	return i.Run.Run(nil, "wg", "set", i.Name, "peer", publicKey, "allowed-ips", addr.String()+"/32")
}

// RemovePeer ends a session.
func (i Interface) RemovePeer(publicKey string) error {
	if err := i.check(); err != nil {
		return err
	}
	if err := checkKey(publicKey); err != nil {
		return err
	}
	return i.Run.Run(nil, "wg", "set", i.Name, "peer", publicKey, "remove")
}

// Tunnel describes one client tunnel.
type Tunnel struct {
	PrivateKey []byte
	NodeKey    string
	Endpoint   netip.AddrPort
	Address    netip.Addr
	// Allowed lists the addresses sent through the tunnel. A route is added
	// for each; 0.0.0.0/0 makes the tunnel the default route.
	Allowed []netip.Prefix
	// MTU sets the interface MTU; 0 keeps WireGuard's default. A tunnel
	// carried inside another tunnel needs a smaller one.
	MTU int
}

// SetUpTunnel creates a client interface as described.
func (i Interface) SetUpTunnel(t Tunnel) error {
	if err := i.check(); err != nil {
		return err
	}
	if len(t.PrivateKey) != 32 {
		return fmt.Errorf("wg: private key must be 32 bytes")
	}
	if err := checkKey(t.NodeKey); err != nil {
		return err
	}
	if !t.Endpoint.IsValid() || !t.Address.Is4() || len(t.Allowed) == 0 {
		return fmt.Errorf("wg: bad endpoint, address or allowed list")
	}
	if t.MTU != 0 && (t.MTU < 1280 || t.MTU > 9000) {
		return fmt.Errorf("wg: MTU %d out of range", t.MTU)
	}
	var allowed []string
	for _, p := range t.Allowed {
		if !p.IsValid() || !p.Addr().Is4() {
			return fmt.Errorf("wg: allowed addresses must be IPv4 prefixes")
		}
		allowed = append(allowed, p.Masked().String())
	}
	_ = i.Delete()
	key := []byte(base64.StdEncoding.EncodeToString(t.PrivateKey))
	steps := []struct {
		stdin []byte
		args  []string
	}{
		{nil, []string{"ip", "link", "add", i.Name, "type", "wireguard"}},
		{key, []string{"wg", "set", i.Name, "private-key", "/dev/stdin",
			"peer", t.NodeKey, "endpoint", t.Endpoint.String(), "allowed-ips", strings.Join(allowed, ","), "persistent-keepalive", "25"}},
		{nil, []string{"ip", "address", "add", t.Address.String() + "/32", "dev", i.Name}},
	}
	if t.MTU != 0 {
		steps = append(steps, struct {
			stdin []byte
			args  []string
		}{nil, []string{"ip", "link", "set", i.Name, "mtu", strconv.Itoa(t.MTU)}})
	}
	steps = append(steps, struct {
		stdin []byte
		args  []string
	}{nil, []string{"ip", "link", "set", i.Name, "up"}})
	for _, p := range t.Allowed {
		dest := p.Masked().String()
		if p.Bits() == 0 {
			dest = "default"
		}
		steps = append(steps, struct {
			stdin []byte
			args  []string
		}{nil, []string{"ip", "route", "replace", dest, "dev", i.Name}})
	}
	for _, s := range steps {
		if err := i.Run.Run(s.stdin, s.args[0], s.args[1:]...); err != nil {
			_ = i.Delete()
			return err
		}
	}
	return nil
}

// SetUpClient creates a single-hop client interface and sends all traffic
// through it. The route to the node itself stays on the existing network,
// because a more specific route to it already exists.
func (i Interface) SetUpClient(privateKey []byte, nodeKey string, endpoint netip.AddrPort, address netip.Addr) error {
	return i.SetUpTunnel(Tunnel{
		PrivateKey: privateKey, NodeKey: nodeKey, Endpoint: endpoint, Address: address,
		Allowed: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
	})
}
