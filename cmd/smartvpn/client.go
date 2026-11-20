package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/flynn/noise"
	"github.com/mspvantage/smartnodes/vpn/control"
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
}

func killSwitch() guard.KillSwitch {
	return guard.KillSwitch{Run: wg.Exec{}, Tunnels: []string{outerIface, innerIface}}
}

func pubOf(k noise.DHKey) string { return base64.StdEncoding.EncodeToString(k.Public) }

// open brings up both tunnels. On failure it puts the machine back as it was.
func (c *client) open() (err error) {
	if err := prepareRoutes(c.entry.endpoint.Addr()); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			teardown()
		}
	}()
	// From here on nothing leaves outside the tunnels.
	if err := killSwitch().Apply(c.entry.endpoint); err != nil {
		return fmt.Errorf("kill switch: %w", err)
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

	// Hop two: the exit node, through the entry. A saved credit first.
	reply, err = c.useCredit()
	if err != nil {
		return err
	}
	if !reply.OK {
		if reply, err = c.buyAndWait(); err != nil {
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
	closeSessions(6 * time.Second)
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
