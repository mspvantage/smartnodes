// Package node is the heart of the SmartNodes node program: it checks the
// tickets clients present, redeems them on chain, and opens and closes
// WireGuard sessions.
//
// A node is paid before it serves. Tickets that arrive close together are
// gathered for about a second and redeemed in one transaction, which keeps
// the node's account from sending two transactions at once. A session opens
// only once the chain has accepted its tickets, so a ticket copied to
// several nodes pays, and serves, exactly once.
//
// An exit node can also be paid ahead: the client prepays for a session key
// it will use later, and the node holds a credit for that key, in memory,
// for up to a day. Opening the session from the credit needs no chain step,
// so the chain never shows when an exit session started.
//
// Privacy rules this package keeps:
//
//   - Nothing here is written to disk. Sessions live in memory and are
//     forgotten when they end or the program stops.
//   - Clients' network addresses never reach this package.
package node

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/mspvantage/smartnodes/vpn/control"
	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
)

// IssuerKey is what the node needs to know about a ticket issuer key.
type IssuerKey struct {
	Pubkey []byte
	// RedeemEnd is the deadline for redeeming its tickets, in Unix seconds.
	RedeemEnd int64
}

// Chain is what the node needs from the chain.
type Chain interface {
	IssuerKey(ctx context.Context, id uint64) (IssuerKey, error)
	TicketSpent(ctx context.Context, keyID uint64, ticketKey []byte) (bool, error)
	// Redeem redeems tickets for this node in one transaction and waits for
	// it to be in a block. If the chain refuses one ticket, it returns a
	// *RedeemError naming it. If the transaction was sent but could not be
	// confirmed, it returns an error wrapping ErrUnconfirmed.
	Redeem(ctx context.Context, keyID uint64, tickets [][]byte) error
}

// RedeemError says which ticket in a batch the chain refused.
type RedeemError struct {
	Index  int
	Reason string
}

func (e *RedeemError) Error() string {
	return "ticket " + strconv.Itoa(e.Index) + " refused: " + e.Reason
}

var (
	// ErrKeyExpired means the issuer key's redemption deadline has passed.
	ErrKeyExpired = errors.New("issuer key has passed its redemption deadline")
	// ErrUnconfirmed means a redemption was sent but its outcome is not
	// known yet.
	ErrUnconfirmed = errors.New("redemption sent but not confirmed")
)

// Tunnel opens and closes WireGuard sessions.
type Tunnel interface {
	AddPeer(publicKey string, addr netip.Addr) error
	RemovePeer(publicKey string) error
}

// Config holds the node's settings.
type Config struct {
	ChainID string
	NodeID  uint64
	// Subnet holds the tunnel addresses. The node uses the first one and
	// hands out the rest.
	Subnet netip.Prefix
	// SessionLength is what one ticket buys.
	SessionLength time.Duration
	// RedeemMargin refuses tickets whose issuer key stops being redeemable
	// sooner than this.
	RedeemMargin time.Duration
	// MaxBatch is the most tickets in one redemption.
	MaxBatch int
	// Gather is how long the node waits for more tickets before sending a
	// redemption.
	Gather time.Duration
	// Wait bounds how long one exchange waits for its redemption.
	Wait time.Duration
	// SettleAfter is how long the node waits before checking the outcome of
	// a redemption it could not confirm.
	SettleAfter time.Duration
	// PaidKeep is how long a ticket that was redeemed but not yet used for
	// a session stays usable, for a client whose exchange timed out.
	PaidKeep time.Duration
	// CreditLife is how long a prepaid credit lasts unused.
	CreditLife time.Duration
	// DNS, if set, is offered to clients as their name server.
	DNS netip.Addr
	// Now returns the current time. Tests replace it.
	Now func() time.Time
}

// Stats are the only figures the node keeps about its work: totals, never
// anything about one session.
type Stats struct {
	Open        int
	Credits     int
	Accepted    uint64
	Prepaid     uint64
	CreditsUsed uint64
	Refused     uint64
	Redeemed    uint64
	Unconfirmed uint64
}

type session struct {
	addr    netip.Addr
	expires time.Time
}

// credit is session time paid ahead for a key.
type credit struct {
	amount  time.Duration
	expires time.Time
}

type redeemReq struct {
	keyID     uint64
	raw       []byte
	ticketKey []byte
	id        string
	done      chan error
}

type cachedKey struct {
	key     IssuerKey
	fetched time.Time
}

const keyCacheTime = 10 * time.Minute

// Node is one running node.
type Node struct {
	cfg   Config
	chain Chain
	tun   Tunnel
	queue chan *redeemReq

	mu       sync.Mutex
	sessions map[string]*session
	used     map[netip.Addr]bool
	// busy holds tickets being redeemed right now.
	busy map[string]bool
	// paid holds tickets this node has redeemed but not yet turned into a
	// session, with when they were redeemed.
	paid map[string]time.Time
	// credits holds prepaid session time by session key.
	credits map[string]*credit
	keys    map[uint64]cachedKey
	stats   Stats
}

// New makes a node. Call Run to start redeeming.
func New(cfg Config, chain Chain, tun Tunnel) (*Node, error) {
	if !cfg.Subnet.IsValid() || !cfg.Subnet.Addr().Is4() || cfg.Subnet.Bits() > 30 {
		return nil, errors.New("node: subnet must be an IPv4 prefix of /30 or larger")
	}
	if cfg.SessionLength <= 0 {
		return nil, errors.New("node: session length must be positive")
	}
	if cfg.MaxBatch <= 0 {
		return nil, errors.New("node: batch size must be positive")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Gather <= 0 {
		cfg.Gather = time.Second
	}
	if cfg.Wait <= 0 {
		cfg.Wait = 25 * time.Second
	}
	if cfg.SettleAfter <= 0 {
		cfg.SettleAfter = 30 * time.Second
	}
	if cfg.PaidKeep <= 0 {
		cfg.PaidKeep = time.Hour
	}
	if cfg.CreditLife <= 0 {
		cfg.CreditLife = 24 * time.Hour
	}
	cfg.Subnet = cfg.Subnet.Masked()
	return &Node{
		cfg:      cfg,
		chain:    chain,
		tun:      tun,
		queue:    make(chan *redeemReq, 1024),
		sessions: map[string]*session{},
		used:     map[netip.Addr]bool{},
		busy:     map[string]bool{},
		paid:     map[string]time.Time{},
		credits:  map[string]*credit{},
		keys:     map[uint64]cachedKey{},
	}, nil
}

// Address is the node's own address inside the tunnel subnet.
func (n *Node) Address() netip.Prefix {
	return netip.PrefixFrom(n.cfg.Subnet.Addr().Next(), n.cfg.Subnet.Bits())
}

func ticketID(keyID uint64, ticketKey []byte) string {
	return strconv.FormatUint(keyID, 10) + "/" + hex.EncodeToString(ticketKey)
}

func (n *Node) refuse(format string, args ...any) control.Reply {
	n.mu.Lock()
	n.stats.Refused++
	n.mu.Unlock()
	return control.Reply{Error: fmt.Sprintf(format, args...)}
}

func (n *Node) issuerKey(ctx context.Context, id uint64) (IssuerKey, error) {
	now := n.cfg.Now()
	n.mu.Lock()
	c, ok := n.keys[id]
	n.mu.Unlock()
	if ok && now.Sub(c.fetched) < keyCacheTime {
		return c.key, nil
	}
	key, err := n.chain.IssuerKey(ctx, id)
	if err != nil {
		return IssuerKey{}, err
	}
	n.mu.Lock()
	n.keys[id] = cachedKey{key: key, fetched: now}
	n.mu.Unlock()
	return key, nil
}

// HandleHello checks the tickets in a Hello, has them redeemed, and opens or
// extends the session for the Hello's WireGuard key. It is the control
// exchange's handler.
func (n *Node) HandleHello(h control.Hello) control.Reply {
	if len(h.Tickets) == 0 {
		if h.Prepay {
			return n.refuse("a prepayment needs tickets")
		}
		return n.useCredit(h.WGPubkey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), n.cfg.Wait)
	defer cancel()

	key, err := n.issuerKey(ctx, h.KeyID)
	if err != nil {
		return n.refuse("unknown issuer key %d", h.KeyID)
	}
	if n.cfg.Now().Add(n.cfg.RedeemMargin).Unix() >= key.RedeemEnd {
		return n.refuse("tickets under issuer key %d can no longer be redeemed", h.KeyID)
	}

	// Check every ticket before redeeming any of them.
	type ticket struct {
		raw []byte
		key []byte
		id  string
	}
	var tickets []ticket
	inHello := map[string]bool{}
	for i, text := range h.Tickets {
		raw, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return n.refuse("ticket %d is not valid base64", i)
		}
		tk, err := ticketcrypto.VerifyTicket(key.Pubkey, n.cfg.ChainID, h.KeyID, n.cfg.NodeID, raw)
		if err != nil {
			return n.refuse("ticket %d is not valid for this node", i)
		}
		id := ticketID(h.KeyID, tk)
		if inHello[id] {
			return n.refuse("ticket %d appears twice", i)
		}
		inHello[id] = true
		tickets = append(tickets, ticket{raw: raw, key: tk, id: id})
	}

	// Tickets this node has already been paid for skip the chain. The rest
	// are reserved so a second exchange cannot use them at the same time.
	var toRedeem []*redeemReq
	n.mu.Lock()
	for i, t := range tickets {
		if _, ok := n.paid[t.id]; ok {
			continue
		}
		if n.busy[t.id] {
			n.mu.Unlock()
			n.release(toRedeem)
			return n.refuse("ticket %d is already being used", i)
		}
		n.busy[t.id] = true
		toRedeem = append(toRedeem, &redeemReq{keyID: h.KeyID, raw: t.raw, ticketKey: t.key, id: t.id, done: make(chan error, 1)})
	}
	n.mu.Unlock()

	// A ticket the chain already holds as spent is refused without sending
	// anything. Asking leaves no trace on the chain.
	for i, r := range toRedeem {
		spent, err := n.chain.TicketSpent(ctx, r.keyID, r.ticketKey)
		if err != nil || spent {
			n.release(toRedeem)
			if err != nil {
				return n.refuse("could not check the tickets; try again")
			}
			return n.refuse("ticket %d has already been used", i)
		}
	}

	// Redeem, and wait until the chain has accepted every ticket.
	for _, r := range toRedeem {
		select {
		case n.queue <- r:
		default:
			n.release(toRedeem)
			return n.refuse("this node is busy; try again")
		}
	}
	var failure error
	for _, r := range toRedeem {
		select {
		case err := <-r.done:
			if err != nil && failure == nil {
				failure = err
			}
		case <-ctx.Done():
			// The redemption carries on. If it succeeds, the client can
			// send the same tickets again and be served without paying
			// twice.
			return n.refuse("the chain is slow; send the same ticket again shortly")
		}
	}
	if failure != nil {
		var re *RedeemError
		switch {
		case errors.As(failure, &re):
			return n.refuse("a ticket was refused by the chain: %s", re.Reason)
		case errors.Is(failure, ErrUnconfirmed):
			return n.refuse("the chain is slow; send the same ticket again shortly")
		default:
			return n.refuse("could not redeem the tickets; try again")
		}
	}

	// Every ticket is paid. Turn them into session time, now or later.
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, t := range tickets {
		if _, ok := n.paid[t.id]; !ok {
			// Another exchange used it in the meantime.
			n.stats.Refused++
			return control.Reply{Error: "a ticket has already been used"}
		}
	}
	bought := time.Duration(len(tickets)) * n.cfg.SessionLength
	now := n.cfg.Now()

	if h.Prepay {
		c := n.credits[h.WGPubkey]
		if c == nil || !now.Before(c.expires) {
			c = &credit{}
			n.credits[h.WGPubkey] = c
		}
		c.amount += bought
		c.expires = now.Add(n.cfg.CreditLife)
		for _, t := range tickets {
			delete(n.paid, t.id)
		}
		n.stats.Prepaid += uint64(len(tickets))
		return control.Reply{OK: true, Expires: c.expires.Unix()}
	}

	reply := n.openLocked(h.WGPubkey, bought, now)
	if !reply.OK {
		n.stats.Refused++
		reply.Error += "; your tickets stay valid here for an hour"
		return reply
	}
	for _, t := range tickets {
		delete(n.paid, t.id)
	}
	n.stats.Accepted += uint64(len(tickets))
	return reply
}

// useCredit opens or extends a session from time paid ahead for the key.
func (n *Node) useCredit(pub string) control.Reply {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.cfg.Now()
	c := n.credits[pub]
	if c == nil || !now.Before(c.expires) {
		delete(n.credits, pub)
		n.stats.Refused++
		return control.Reply{Error: "no credit is held for this key"}
	}
	reply := n.openLocked(pub, c.amount, now)
	if !reply.OK {
		// The credit stays for a later try.
		n.stats.Refused++
		return reply
	}
	delete(n.credits, pub)
	n.stats.CreditsUsed++
	return reply
}

// openLocked opens a session for the key, or extends it, by the given time.
func (n *Node) openLocked(pub string, d time.Duration, now time.Time) control.Reply {
	s := n.sessions[pub]
	if s == nil {
		addr, ok := n.allocateLocked()
		if !ok {
			return control.Reply{Error: "this node is full"}
		}
		if err := n.tun.AddPeer(pub, addr); err != nil {
			return control.Reply{Error: "this node could not open the tunnel"}
		}
		n.used[addr] = true
		s = &session{addr: addr, expires: now}
		n.sessions[pub] = s
	}
	from := s.expires
	if from.Before(now) {
		from = now
	}
	s.expires = from.Add(d)
	reply := control.Reply{OK: true, TunnelIP: s.addr.String(), Expires: s.expires.Unix()}
	if n.cfg.DNS.IsValid() {
		reply.DNS = n.cfg.DNS.String()
	}
	return reply
}

// release frees reserved tickets that were not sent for redemption.
func (n *Node) release(rs []*redeemReq) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, r := range rs {
		delete(n.busy, r.id)
	}
}

// Run redeems queued tickets until ctx ends. It gathers tickets for a moment
// and sends them in one transaction, so the node never sends two
// transactions at once.
func (n *Node) Run(ctx context.Context) {
	for {
		var batch []*redeemReq
		select {
		case <-ctx.Done():
			return
		case r := <-n.queue:
			batch = append(batch, r)
		}
		timer := time.NewTimer(n.cfg.Gather)
	gather:
		for len(batch) < n.cfg.MaxBatch {
			select {
			case r := <-n.queue:
				batch = append(batch, r)
			case <-timer.C:
				break gather
			case <-ctx.Done():
				break gather
			}
		}
		timer.Stop()
		n.redeem(ctx, batch)
	}
}

// redeem sends one gathered batch, grouped by issuer key.
func (n *Node) redeem(ctx context.Context, batch []*redeemReq) {
	groups := map[uint64][]*redeemReq{}
	var order []uint64
	for _, r := range batch {
		if _, ok := groups[r.keyID]; !ok {
			order = append(order, r.keyID)
		}
		groups[r.keyID] = append(groups[r.keyID], r)
	}
	for _, keyID := range order {
		n.redeemGroup(ctx, keyID, groups[keyID])
	}
}

func (n *Node) redeemGroup(ctx context.Context, keyID uint64, group []*redeemReq) {
	for len(group) > 0 {
		raws := make([][]byte, len(group))
		for i, r := range group {
			raws[i] = r.raw
		}
		err := n.chain.Redeem(ctx, keyID, raws)
		var re *RedeemError
		switch {
		case err == nil:
			n.settle(group, nil)
			return
		case errors.As(err, &re) && re.Index >= 0 && re.Index < len(group):
			// One ticket is bad. Refuse it and try the rest again.
			n.settle(group[re.Index:re.Index+1], err)
			group = append(group[:re.Index:re.Index], group[re.Index+1:]...)
		case errors.Is(err, ErrUnconfirmed):
			n.settle(group, err)
			return
		default:
			n.settle(group, err)
			return
		}
	}
}

// settle records the outcome of a redemption and tells the waiting
// exchanges.
func (n *Node) settle(rs []*redeemReq, err error) {
	n.mu.Lock()
	now := n.cfg.Now()
	for _, r := range rs {
		switch {
		case err == nil:
			n.paid[r.id] = now
			delete(n.busy, r.id)
			n.stats.Redeemed++
		case errors.Is(err, ErrUnconfirmed):
			// Keep it reserved and look again later.
			n.stats.Unconfirmed++
			go n.checkLater(r)
		default:
			delete(n.busy, r.id)
		}
	}
	n.mu.Unlock()
	for _, r := range rs {
		r.done <- err
	}
}

// checkLater finds out whether an unconfirmed redemption went through. If it
// did, the ticket is kept as paid so the client can use it.
func (n *Node) checkLater(r *redeemReq) {
	time.Sleep(n.cfg.SettleAfter)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spent, err := n.chain.TicketSpent(ctx, r.keyID, r.ticketKey)
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.busy, r.id)
	if err == nil && spent {
		// Only this node can redeem a ticket made out to it, so it was ours.
		n.paid[r.id] = n.cfg.Now()
		n.stats.Redeemed++
	}
}

// allocateLocked finds a free tunnel address. The first address of the
// subnet is the network, the second is the node, the last is broadcast.
func (n *Node) allocateLocked() (netip.Addr, bool) {
	for a := n.cfg.Subnet.Addr().Next().Next(); n.cfg.Subnet.Contains(a.Next()); a = a.Next() {
		if !n.used[a] {
			return a, true
		}
	}
	return netip.Addr{}, false
}

func (n *Node) endLocked(pub string) {
	s := n.sessions[pub]
	if s == nil {
		return
	}
	_ = n.tun.RemovePeer(pub)
	delete(n.used, s.addr)
	delete(n.sessions, pub)
}

// Expire ends every session whose time is up, forgets paid tickets nobody
// came back for, and returns how many sessions it ended.
func (n *Node) Expire() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.cfg.Now()
	ended := 0
	for pub, s := range n.sessions {
		if !now.Before(s.expires) {
			n.endLocked(pub)
			ended++
		}
	}
	for id, at := range n.paid {
		if now.Sub(at) >= n.cfg.PaidKeep {
			delete(n.paid, id)
		}
	}
	for pub, c := range n.credits {
		if !now.Before(c.expires) {
			delete(n.credits, pub)
		}
	}
	return ended
}

// Stats returns the node's totals.
func (n *Node) Stats() Stats {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.stats
	s.Open = len(n.sessions)
	s.Credits = len(n.credits)
	return s
}
