package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/flynn/noise"
	"github.com/mspvantage/smartnodes/vpn/control"
	"github.com/mspvantage/smartnodes/vpn/disguise"
	"github.com/mspvantage/smartnodes/vpn/guard"
	"github.com/mspvantage/smartnodes/vpn/wg"
)

// client is one two-hop connection.
type client struct {
	chainID string
	keyID   uint64
	entry   peer
	exit    peer
	// pay makes out one ticket to a node.
	pay func(nodeID uint64) (string, error)
	// creditPath is where a credit for this exit is kept between sessions;
	// empty means none.
	creditPath string
	// wait is the longest random wait between paying an exit and using it.
	wait time.Duration
	say  func(format string, args ...any)

	outer, inner        noise.DHKey
	entryEnds, exitEnds time.Time

	// transport is how the entry is reached: "wireguard", "reality", or
	// "auto", which tries WireGuard and falls back to the disguise.
	transport string
	// reality is the entry's published disguise, if it offers one.
	reality *disguise.Reality
	xray    string
	// direct is the entry as published; entry may instead point at the
	// local end of the disguise.
	direct peer
	// target is where the disguise delivers on the entry: its WireGuard and
	// payment port.
	target netip.AddrPort
	// memory remembers that WireGuard was blocked here; see blockedHere.
	memory string
}

// errEntryBlocked means WireGuard to the entry node seems to be blocked on
// the way: the node could not be reached at all, or its tunnel never
// answered.
var errEntryBlocked = errors.New("WireGuard to the entry node seems blocked")

// open brings up both tunnels, through the disguise if asked or if
// WireGuard turns out to be blocked. On failure it puts the machine back as
// it was.
func (c *client) open() error {
	switch c.transport {
	case "reality":
		if c.reality == nil {
			return errors.New("the entry node offers no disguise")
		}
		return c.openWith(true)
	case "auto":
		if c.reality != nil && blockedHere(c.memory, time.Now()) {
			// WireGuard was blocked here lately: go straight to the
			// disguise, and spend no ticket finding out again.
			c.say("WireGuard was blocked on this network lately; using the disguise")
			err := c.openWith(true)
			if err == nil || !unreachable(err, c.entry.id) {
				return err
			}
			c.say("the disguise did not get through (%v); trying WireGuard", err)
		}
		err := c.openWith(false)
		if errors.Is(err, errEntryBlocked) && c.reality != nil {
			c.say("%v; trying the disguise instead", err)
			if err = c.openWith(true); err == nil {
				rememberBlocked(c.memory, time.Now())
			}
			return err
		}
		if err == nil {
			forgetBlocked(c.memory)
		}
		return err
	default:
		return c.openWith(false)
	}
}

// handshaken reports whether a WireGuard interface has completed a
// handshake with its peer.
func handshaken(name string) bool {
	out, err := output("wg", "show", name, "latest-handshakes")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] != "0" {
			return true
		}
	}
	return false
}

// reached reports whether an error from asking a node means the node was
// never reached, as when something on the way blocks it.
func unreachable(err error, id uint64) bool {
	s := err.Error()
	return strings.Contains(s, fmt.Sprintf("cannot reach node %d:", id)) || strings.Contains(s, fmt.Sprintf("node %d did not answer properly:", id))
}

// startXray runs the device's end of the disguise and points the entry at
// it.
func (c *client) startXray() error {
	xray, err := exec.LookPath(c.xray)
	if err != nil {
		return fmt.Errorf("the Xray program was not found (%s); install it or give -xray", c.xray)
	}
	local, err := freeLocalPort()
	if err != nil {
		return err
	}
	cfg, err := disguise.ClientConfig(*c.reality, c.direct.endpoint.Addr(), local, c.target)
	if err != nil {
		return err
	}
	path, err := disguise.WriteConfig(filepath.Dir(stateFile), filepath.Base(stateFile)+".xray.json", cfg)
	if err != nil {
		return err
	}
	pid, err := disguise.StartDetached(xray, path)
	if err != nil {
		return err
	}
	st, err := readState()
	if err != nil {
		return err
	}
	st.XrayPID, st.Xray, st.XrayConfig = pid, xray, path
	if err := writeState(st); err != nil {
		return err
	}
	if err := disguise.WaitListening(local.String(), 10*time.Second); err != nil {
		return err
	}
	c.entry.endpoint = local
	c.say("reaching entry node %d through the disguise, looking like a visit to %s", c.entry.id, c.reality.ServerName)
	return nil
}

// freeLocalPort finds a loopback port free for both TCP and UDP.
func freeLocalPort() (netip.AddrPort, error) {
	for i := 0; i < 20; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return netip.AddrPort{}, err
		}
		addr := l.Addr().String()
		u, err := net.ListenPacket("udp", addr)
		l.Close()
		if err != nil {
			continue
		}
		u.Close()
		return netip.ParseAddrPort(addr)
	}
	return netip.AddrPort{}, errors.New("no free local port")
}

func killSwitch() guard.KillSwitch {
	return guard.KillSwitch{Run: wg.Exec{}, Tunnels: []string{outerIface, innerIface}}
}

func pubOf(k noise.DHKey) string { return base64.StdEncoding.EncodeToString(k.Public) }

// openWith brings up both tunnels, through the disguise or not.
func (c *client) openWith(disguised bool) (err error) {
	c.entry = c.direct
	if err := prepareRoutes(c.direct.endpoint.Addr()); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			teardown()
		}
	}()
	// From here on nothing leaves outside the tunnels. Through the
	// disguise, the only way out is its TCP port at the entry.
	allow := c.direct.endpoint
	if disguised {
		allow = netip.AddrPortFrom(c.direct.endpoint.Addr(), c.reality.Port)
	}
	if err := killSwitch().Apply(allow); err != nil {
		return fmt.Errorf("kill switch: %w", err)
	}
	if disguised {
		if err := c.startXray(); err != nil {
			return err
		}
	}

	// Hop one: the entry node.
	if c.outer, err = control.NewKey(); err != nil {
		return err
	}
	ticket, err := c.pay(c.entry.id)
	if err != nil {
		return err
	}
	reply, err := ask(c.chainID, c.entry, control.Hello{KeyID: c.keyID, Tickets: strings.Fields(ticket), WGPubkey: pubOf(c.outer)})
	if err != nil {
		if !disguised && unreachable(err, c.entry.id) {
			return fmt.Errorf("%w (%v)", errEntryBlocked, err)
		}
		return err
	}
	outerAddr, err := tunnelAddr(reply)
	if err != nil {
		return err
	}
	if err := iface(outerIface).SetUpTunnel(wg.Tunnel{
		PrivateKey: c.outer.Private, NodeKey: c.entry.key, Endpoint: c.entry.endpoint, Address: outerAddr,
		Allowed: []netip.Prefix{netip.PrefixFrom(c.exit.endpoint.Addr(), 32)},
	}); err != nil {
		return err
	}
	if err := run("ip", "route", "replace", "default", "dev", outerIface); err != nil {
		return err
	}
	c.entryEnds = time.Unix(reply.Expires, 0)
	_ = rememberClose(closeTarget{ChainID: c.chainID, NodeID: c.entry.id, NodeKey: c.entry.key, Endpoint: c.entry.endpoint.String(), WGPubkey: pubOf(c.outer)})
	c.say("entry node %d: tunnel address %s", c.entry.id, outerAddr)

	// Before paying the exit, check the entry's tunnel carries anything at
	// all: a censor may block WireGuard yet let the payment connection
	// through. Opening a connection to the exit makes the tunnel shake
	// hands; no ticket is spent on it.
	if !disguised {
		if c2, err := net.DialTimeout("tcp", c.exit.endpoint.String(), 8*time.Second); err == nil {
			c2.Close()
		} else if !handshaken(outerIface) {
			return fmt.Errorf("%w (its tunnel never answered)", errEntryBlocked)
		}
	}

	// Hop two: the exit node, through the entry. A saved credit first.
	reply, err = c.useCredit()
	if err != nil {
		return err
	}
	if !reply.OK {
		if reply, err = c.buyAndWait(); err != nil {
			// The exit is reached through the entry's tunnel. If that
			// tunnel never answered, the fault is on the way to the entry.
			if !disguised && unreachable(err, c.exit.id) && !handshaken(outerIface) {
				return fmt.Errorf("%w (its tunnel never answered)", errEntryBlocked)
			}
			return err
		}
	}
	innerAddr, err := tunnelAddr(reply)
	if err != nil {
		return err
	}
	if err := iface(innerIface).SetUpTunnel(wg.Tunnel{
		PrivateKey: c.inner.Private, NodeKey: c.exit.key, Endpoint: c.exit.endpoint, Address: innerAddr,
		Allowed: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, MTU: innerMTU,
	}); err != nil {
		return err
	}
	if reply.DNS != "" {
		if err := setResolver(reply.DNS); err != nil {
			return err
		}
		c.say("name lookups go to the exit's name server %s", reply.DNS)
	}
	c.exitEnds = time.Unix(reply.Expires, 0)
	_ = rememberClose(closeTarget{ChainID: c.chainID, NodeID: c.exit.id, NodeKey: c.exit.key, Endpoint: c.exit.endpoint.String(), WGPubkey: pubOf(c.inner)})
	c.say("exit node %d: tunnel address %s", c.exit.id, innerAddr)
	c.say("connected through two hops; entry session ends %s, exit session ends %s", clock(c.entryEnds), clock(c.exitEnds))
	return nil
}

// useCredit opens the exit session from a saved credit, if there is one.
// A refused credit (the exit restarted, or it lapsed) is not an error: the
// reply comes back not OK and the caller buys a new one, so the entry
// ticket already spent is not wasted.
func (c *client) useCredit() (control.Reply, error) {
	if c.creditPath == "" {
		return control.Reply{}, nil
	}
	cr, err := loadCredit(c.creditPath, time.Now())
	if errors.Is(err, os.ErrNotExist) {
		return control.Reply{}, nil
	}
	_ = os.Remove(c.creditPath) // a credit is tried once
	if err != nil {
		c.say("saved credit not usable (%v); buying a new one", err)
		return control.Reply{}, nil
	}
	if cr.ChainID != c.chainID || cr.ExitID != c.exit.id || cr.ExitKey != c.exit.key {
		c.say("saved credit is for another exit; buying a new one")
		return control.Reply{}, nil
	}
	priv, err := base64.StdEncoding.DecodeString(cr.WGPrivate)
	if err != nil {
		return control.Reply{}, nil
	}
	if c.inner, err = control.KeyFromPrivate(priv); err != nil {
		return control.Reply{}, nil
	}
	reply, err := ask(c.chainID, c.exit, control.Hello{KeyID: c.keyID, WGPubkey: pubOf(c.inner)})
	if err != nil {
		c.say("exit refused the saved credit (%v); buying a new one", err)
		return control.Reply{}, nil
	}
	c.say("exit node %d opened from a saved credit: no payment now, nothing on the chain", c.exit.id)
	return reply, nil
}

// buyAndWait pays the exit ahead, waits a random time, then opens the
// session, so the chain cannot pair this exit payment with the entry's.
func (c *client) buyAndWait() (control.Reply, error) {
	k, err := control.NewKey()
	if err != nil {
		return control.Reply{}, err
	}
	ticket, err := c.pay(c.exit.id)
	if err != nil {
		return control.Reply{}, err
	}
	if _, err := ask(c.chainID, c.exit, control.Hello{KeyID: c.keyID, Tickets: strings.Fields(ticket), WGPubkey: pubOf(k), Prepay: true}); err != nil {
		return control.Reply{}, err
	}
	d := randomWait(c.wait).Round(time.Second)
	c.say("exit node %d paid; waiting %s before using it, so the chain cannot link the two", c.exit.id, d)
	time.Sleep(d)
	c.inner = k
	return ask(c.chainID, c.exit, control.Hello{KeyID: c.keyID, WGPubkey: pubOf(k)})
}

// renewEntry and renewExit add a ticket's worth of time to a session.
func (c *client) renewEntry() error {
	ticket, err := c.pay(c.entry.id)
	if err != nil {
		return err
	}
	r, err := ask(c.chainID, c.entry, control.Hello{KeyID: c.keyID, Tickets: []string{ticket}, WGPubkey: pubOf(c.outer)})
	if err != nil {
		return err
	}
	c.entryEnds = time.Unix(r.Expires, 0)
	return nil
}

func (c *client) renewExit() error {
	ticket, err := c.pay(c.exit.id)
	if err != nil {
		return err
	}
	r, err := ask(c.chainID, c.exit, control.Hello{KeyID: c.keyID, Tickets: []string{ticket}, WGPubkey: pubOf(c.inner)})
	if err != nil {
		return err
	}
	c.exitEnds = time.Unix(r.Expires, 0)
	return nil
}

// buyCredit pays the exit ahead for the next session.
func (c *client) buyCredit() error {
	k, err := control.NewKey()
	if err != nil {
		return err
	}
	ticket, err := c.pay(c.exit.id)
	if err != nil {
		return err
	}
	r, err := ask(c.chainID, c.exit, control.Hello{KeyID: c.keyID, Tickets: []string{ticket}, WGPubkey: pubOf(k), Prepay: true})
	if err != nil {
		return err
	}
	return saveCredit(c.creditPath, creditFile{
		ChainID: c.chainID, ExitID: c.exit.id, ExitKey: c.exit.key, Endpoint: c.exit.endpoint.String(),
		WGPrivate: base64.StdEncoding.EncodeToString(k.Private), Expires: r.Expires,
	})
}

// teardown tells the nodes the sessions are over, then removes both tunnels
// and the kill switch and puts routes and name servers back.
func teardown() error {
	st, _ := readState()
	closeSessions(6 * time.Second)
	defer func() {
		if st.XrayPID != 0 {
			disguise.StopDetached(st.XrayPID, st.Xray)
			_ = os.Remove(st.XrayConfig)
		}
	}()
	_ = iface(innerIface).Delete()
	_ = iface(outerIface).Delete()
	killSwitch().Remove()
	return restoreRoutes()
}

// closeSessions asks each node to end its session, all at once, waiting at
// most d. The exit is reached through the tunnels, which are still up. A
// node that does not answer just lets the session run out.
func closeSessions(d time.Duration) {
	st, err := readState()
	if err != nil || len(st.Close) == 0 {
		return
	}
	done := make(chan struct{}, len(st.Close))
	for _, t := range st.Close {
		go func(t closeTarget) {
			defer func() { done <- struct{}{} }()
			p, err := newPeer(t.NodeID, t.NodeKey, t.Endpoint)
			if err != nil {
				return
			}
			_, _ = ask(t.ChainID, p, control.Hello{WGPubkey: t.WGPubkey, Close: true})
		}(t)
	}
	timeout := time.After(d)
	for range st.Close {
		select {
		case <-done:
		case <-timeout:
			return
		}
	}
}

// randomBetween picks a time between lo and hi.
func randomBetween(lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + uniform(hi-lo)
}

// renewAt picks when to renew a session ending at end: somewhere between
// half and four fifths of the time left, chosen afresh each time, so entry
// and exit renewals do not keep step.
func renewAt(now, end time.Time) time.Time {
	left := end.Sub(now)
	if left <= 0 {
		return now
	}
	return now.Add(randomBetween(left/2, left*4/5))
}

func clock(t time.Time) string { return t.Format("15:04:05") }

// blockedFor is how long the device remembers that WireGuard was blocked.
const blockedFor = 24 * time.Hour

// The memory file holds only a time: not which node, nor which network.
type transportMemory struct {
	WireGuardBlockedUntil int64 `json:"wireguard_blocked_until"`
}

// blockedHere reports whether WireGuard was found blocked lately.
func blockedHere(path string, now time.Time) bool {
	if path == "" {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var m transportMemory
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	return now.Unix() < m.WireGuardBlockedUntil
}

func rememberBlocked(path string, now time.Time) {
	if path == "" {
		return
	}
	raw, _ := json.Marshal(transportMemory{WireGuardBlockedUntil: now.Add(blockedFor).Unix()})
	_ = os.WriteFile(path, raw, 0o600)
}

func forgetBlocked(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}
