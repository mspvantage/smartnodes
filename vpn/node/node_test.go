package node

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/mspvantage/smartnodes/vpn/control"
	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
)

const (
	chainID = "smartnodes-test"
	nodeID  = uint64(4)
	keyID   = uint64(2)
)

// fakeChain redeems like the real tickets module: a transaction with any
// spent ticket is refused whole, naming the ticket.
type fakeChain struct {
	mu      sync.Mutex
	keys    map[uint64]IssuerKey
	spent   map[string]bool
	txs     int
	sizes   []int
	down    bool
	expired bool
	// unconfirmed makes the next redemption go through but report
	// ErrUnconfirmed, as when a block is slow.
	unconfirmed bool
	// delay holds every redemption back, as a slow block would.
	delay time.Duration
	// before runs once, at the start of the next redemption: it stands for
	// another node being paid a copied ticket just before this one.
	before func()
}

func newFakeChain(pk []byte) *fakeChain {
	return &fakeChain{keys: map[uint64]IssuerKey{keyID: {Pubkey: pk, RedeemEnd: 4_000_000_000}}, spent: map[string]bool{}}
}

func (c *fakeChain) IssuerKey(_ context.Context, id uint64) (IssuerKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k, ok := c.keys[id]
	if !ok {
		return IssuerKey{}, errors.New("not found")
	}
	return k, nil
}

func (c *fakeChain) TicketSpent(_ context.Context, keyID uint64, tk []byte) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.down {
		return false, errors.New("unreachable")
	}
	return c.spent[ticketID(keyID, tk)], nil
}

func (c *fakeChain) markSpent(raw []byte) {
	c.mu.Lock()
	c.spent[ticketID(keyID, raw[:ticketcrypto.TicketKeySize])] = true
	c.mu.Unlock()
}

func (c *fakeChain) Redeem(_ context.Context, keyID uint64, tickets [][]byte) error {
	c.mu.Lock()
	delay, before := c.delay, c.before
	c.before = nil
	c.mu.Unlock()
	time.Sleep(delay)
	if before != nil {
		before()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.txs++
	c.sizes = append(c.sizes, len(tickets))
	if c.down {
		return errors.New("unreachable")
	}
	if c.expired {
		return ErrKeyExpired
	}
	for i, t := range tickets {
		if c.spent[ticketID(keyID, t[:ticketcrypto.TicketKeySize])] {
			return &RedeemError{Index: i, Reason: "ticket has already been redeemed"}
		}
	}
	for _, t := range tickets {
		c.spent[ticketID(keyID, t[:ticketcrypto.TicketKeySize])] = true
	}
	if c.unconfirmed {
		c.unconfirmed = false
		return ErrUnconfirmed
	}
	return nil
}

func (c *fakeChain) redeemedTxs() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.txs
}

type fakeTunnel struct {
	mu    sync.Mutex
	peers map[string]netip.Addr
	fail  bool
}

func (t *fakeTunnel) AddPeer(pub string, addr netip.Addr) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fail {
		return errors.New("wg failed")
	}
	t.peers[pub] = addr
	return nil
}

func (t *fakeTunnel) RemovePeer(pub string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, pub)
	return nil
}

func (t *fakeTunnel) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.peers)
}

// wallet issues real tickets with a real issuer key.
type wallet struct{ sk, pk []byte }

func newWallet(t *testing.T) *wallet {
	t.Helper()
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	sk, pk, err := ticketcrypto.GenerateIssuerKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	return &wallet{sk: sk, pk: pk}
}

func (w *wallet) secret(t *testing.T) (*ticketcrypto.TicketSecret, []byte) {
	t.Helper()
	s, err := ticketcrypto.NewTicket(rand.Reader, chainID, keyID)
	if err != nil {
		t.Fatal(err)
	}
	blindSig, err := ticketcrypto.BlindSign(w.sk, s.Blinded)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ticketcrypto.Unblind(s, w.pk, chainID, keyID, blindSig)
	if err != nil {
		t.Fatal(err)
	}
	return s, sig
}

// ticket makes a ticket spent to the given node, in base64.
func (w *wallet) ticket(t *testing.T, forNode uint64) string {
	t.Helper()
	s, sig := w.secret(t)
	raw, err := ticketcrypto.Spend(s, sig, chainID, keyID, forNode)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func raw(t *testing.T, b64 string) []byte {
	t.Helper()
	r, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type world struct {
	n      *Node
	chain  *fakeChain
	tun    *fakeTunnel
	w      *wallet
	cancel context.CancelFunc

	mu  sync.Mutex
	now time.Time
}

func (wd *world) clock() time.Time {
	wd.mu.Lock()
	defer wd.mu.Unlock()
	return wd.now
}

func (wd *world) advance(d time.Duration) {
	wd.mu.Lock()
	wd.now = wd.now.Add(d)
	wd.mu.Unlock()
}

func newWorld(t *testing.T, subnet string, tweak ...func(*Config)) *world {
	t.Helper()
	wd := &world{now: time.Unix(1_800_000_000, 0), w: newWallet(t), tun: &fakeTunnel{peers: map[string]netip.Addr{}}}
	wd.chain = newFakeChain(wd.w.pk)
	cfg := Config{
		ChainID:       chainID,
		NodeID:        nodeID,
		Subnet:        netip.MustParsePrefix(subnet),
		SessionLength: time.Hour,
		RedeemMargin:  time.Hour,
		MaxBatch:      3,
		Gather:        20 * time.Millisecond,
		Wait:          2 * time.Second,
		SettleAfter:   50 * time.Millisecond,
		Now:           wd.clock,
	}
	for _, f := range tweak {
		f(&cfg)
	}
	n, err := New(cfg, wd.chain, wd.tun)
	if err != nil {
		t.Fatal(err)
	}
	wd.n = n
	ctx, cancel := context.WithCancel(context.Background())
	wd.cancel = cancel
	go n.Run(ctx)
	t.Cleanup(cancel)
	return wd
}

func wgKey(t *testing.T) string {
	t.Helper()
	k, err := control.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k.Public)
}

func (wd *world) hello(pub string, tickets ...string) control.Reply {
	return wd.n.HandleHello(control.Hello{Version: control.Version, KeyID: keyID, Tickets: tickets, WGPubkey: pub})
}

func TestPaidBeforeServing(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	pub := wgKey(t)
	tk := wd.w.ticket(t, nodeID)
	r := wd.hello(pub, tk)
	if !r.OK {
		t.Fatalf("refused: %s", r.Error)
	}
	if r.TunnelIP != "10.77.0.2" || r.Expires != wd.now.Add(time.Hour).Unix() {
		t.Fatalf("reply %+v", r)
	}
	// By the time the client hears yes, the chain has the ticket.
	if wd.chain.redeemedTxs() != 1 {
		t.Fatal("served before redeeming")
	}
	if spent, _ := wd.chain.TicketSpent(context.Background(), keyID, raw(t, tk)[:32]); !spent {
		t.Fatal("ticket not spent on chain")
	}
	if wd.tun.peers[pub] != netip.MustParseAddr("10.77.0.2") {
		t.Fatal("peer not added")
	}
	if wd.n.Address().String() != "10.77.0.1/24" {
		t.Fatalf("node address %s", wd.n.Address())
	}
	if s := wd.n.Stats(); s.Open != 1 || s.Accepted != 1 || s.Redeemed != 1 {
		t.Fatalf("stats %+v", s)
	}
}

func TestRefusals(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	used := wd.w.ticket(t, nodeID)
	if r := wd.hello(wgKey(t), used); !r.OK {
		t.Fatal(r.Error)
	}
	forged := newWallet(t).ticket(t, nodeID)
	dup := wd.w.ticket(t, nodeID)

	cases := map[string]control.Hello{
		"already used":                   {KeyID: keyID, Tickets: []string{used}},
		"made out to another node":       {KeyID: keyID, Tickets: []string{wd.w.ticket(t, nodeID+1)}},
		"forged":                         {KeyID: keyID, Tickets: []string{forged}},
		"unknown issuer key":             {KeyID: 9, Tickets: []string{wd.w.ticket(t, nodeID)}},
		"not base64":                     {KeyID: keyID, Tickets: []string{"!!"}},
		"cut short":                      {KeyID: keyID, Tickets: []string{wd.w.ticket(t, nodeID)[:40]}},
		"same ticket twice in one hello": {KeyID: keyID, Tickets: []string{dup, dup}},
	}
	for name, h := range cases {
		h.Version = control.Version
		h.WGPubkey = wgKey(t)
		if r := wd.n.HandleHello(h); r.OK || r.Error == "" {
			t.Fatalf("%s: accepted", name)
		}
	}
	if wd.tun.count() != 1 {
		t.Fatalf("refused hellos opened sessions: %d peers", wd.tun.count())
	}
	// Only the one good ticket was ever sent to the chain.
	if wd.chain.redeemedTxs() != 1 {
		t.Fatalf("%d redemptions", wd.chain.redeemedTxs())
	}
	if s := wd.n.Stats(); s.Refused != uint64(len(cases)) {
		t.Fatalf("stats %+v", s)
	}
	// The duplicate was refused whole and is still good.
	if r := wd.hello(wgKey(t), dup); !r.OK {
		t.Fatalf("a ticket from a refused hello could not be used: %s", r.Error)
	}
}

// The same ticket spent at two nodes: whichever redeems second refuses it
// and never opens a session.
func TestCopiedTicketServesOnce(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	s, sig := wd.w.secret(t)
	here, _ := ticketcrypto.Spend(s, sig, chainID, keyID, nodeID)

	// Before this node checks, the other node has already been paid.
	wd.chain.markSpent(here)
	if r := wd.hello(wgKey(t), base64.StdEncoding.EncodeToString(here)); r.OK {
		t.Fatal("served a ticket already redeemed elsewhere")
	}

	// The other node is paid between this node's check and its redemption.
	s2, sig2 := wd.w.secret(t)
	here2, _ := ticketcrypto.Spend(s2, sig2, chainID, keyID, nodeID)
	wd.chain.mu.Lock()
	wd.chain.before = func() { wd.chain.markSpent(here2) }
	wd.chain.mu.Unlock()
	if r := wd.hello(wgKey(t), base64.StdEncoding.EncodeToString(here2)); r.OK {
		t.Fatal("served a ticket the chain refused")
	}
	if wd.tun.count() != 0 {
		t.Fatal("a session was opened")
	}
}

// Tickets arriving together share one transaction; a bad one is refused
// and the rest still go through.
func TestGatheredIntoFewTransactions(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24", func(c *Config) { c.Gather = 500 * time.Millisecond; c.MaxBatch = 10 })
	var tickets []string
	for i := 0; i < 6; i++ {
		tickets = append(tickets, wd.w.ticket(t, nodeID))
	}
	// One of them is spent elsewhere after this node checked it.
	bad := 3
	wd.chain.mu.Lock()
	wd.chain.before = func() { wd.chain.markSpent(raw(t, tickets[bad])) }
	wd.chain.mu.Unlock()

	var wg sync.WaitGroup
	results := make([]control.Reply, len(tickets))
	for i, tk := range tickets {
		wg.Add(1)
		go func(i int, tk string) {
			defer wg.Done()
			results[i] = wd.hello(wgKey(t), tk)
		}(i, tk)
	}
	wg.Wait()

	for i, r := range results {
		if (i == bad) == r.OK {
			t.Fatalf("ticket %d: %+v", i, r)
		}
	}
	// One transaction refused for the bad ticket, one for the rest.
	if txs := wd.chain.redeemedTxs(); txs != 2 {
		t.Fatalf("%d transactions, sizes %v", txs, wd.chain.sizes)
	}
	if s := wd.n.Stats(); s.Open != 5 || s.Redeemed != 5 {
		t.Fatalf("stats %+v", s)
	}
}

func TestBatchSizeLimit(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24", func(c *Config) { c.Gather = 200 * time.Millisecond })
	var wg sync.WaitGroup
	for i := 0; i < 7; i++ {
		tk := wd.w.ticket(t, nodeID)
		wg.Add(1)
		go func() { defer wg.Done(); wd.hello(wgKey(t), tk) }()
	}
	wg.Wait()
	for _, size := range wd.chain.sizes {
		if size > 3 {
			t.Fatalf("a transaction carried %d tickets", size)
		}
	}
	if s := wd.n.Stats(); s.Open != 7 {
		t.Fatalf("stats %+v", s)
	}
}

func TestChainUnreachable(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	tk := wd.w.ticket(t, nodeID)
	wd.chain.mu.Lock()
	wd.chain.down = true
	wd.chain.mu.Unlock()
	if r := wd.hello(wgKey(t), tk); r.OK {
		t.Fatal("served without the chain")
	}
	wd.chain.mu.Lock()
	wd.chain.down = false
	wd.chain.mu.Unlock()
	if r := wd.hello(wgKey(t), tk); !r.OK {
		t.Fatalf("ticket lost after an outage: %s", r.Error)
	}
}

// A redemption that went through but could not be confirmed in time: the
// client is told to retry, and the retry is served without paying twice.
func TestUnconfirmedThenRetry(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	tk := wd.w.ticket(t, nodeID)
	wd.chain.mu.Lock()
	wd.chain.unconfirmed = true
	wd.chain.mu.Unlock()
	if r := wd.hello(wgKey(t), tk); r.OK {
		t.Fatal("served an unconfirmed redemption")
	}
	// While the node is still finding out, the same ticket is held.
	if r := wd.hello(wgKey(t), tk); r.OK {
		t.Fatal("served while unsure")
	}
	time.Sleep(500 * time.Millisecond)
	if r := wd.hello(wgKey(t), tk); !r.OK {
		t.Fatalf("retry refused: %s", r.Error)
	}
	if wd.chain.redeemedTxs() != 1 {
		t.Fatalf("redeemed %d times", wd.chain.redeemedTxs())
	}
	if s := wd.n.Stats(); s.Unconfirmed != 1 || s.Redeemed != 1 || s.Open != 1 {
		t.Fatalf("stats %+v", s)
	}
}

// A client that gives up waiting does not lose its ticket.
func TestClientTimeoutKeepsTicket(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24", func(c *Config) { c.Wait = 50 * time.Millisecond })
	tk := wd.w.ticket(t, nodeID)
	wd.chain.mu.Lock()
	wd.chain.delay = 150 * time.Millisecond
	wd.chain.mu.Unlock()
	if r := wd.hello(wgKey(t), tk); r.OK {
		t.Fatal("expected a timeout")
	}
	time.Sleep(600 * time.Millisecond)
	wd.chain.mu.Lock()
	wd.chain.delay = 0
	wd.chain.mu.Unlock()
	if r := wd.hello(wgKey(t), tk); !r.OK {
		t.Fatalf("retry refused: %s", r.Error)
	}
	if wd.chain.redeemedTxs() != 1 {
		t.Fatalf("redeemed %d times", wd.chain.redeemedTxs())
	}
	// A paid ticket nobody comes back for is forgotten after an hour.
	tk2 := wd.w.ticket(t, nodeID)
	wd.chain.mu.Lock()
	wd.chain.delay = 150 * time.Millisecond
	wd.chain.mu.Unlock()
	wd.hello(wgKey(t), tk2)
	time.Sleep(600 * time.Millisecond)
	wd.advance(time.Hour)
	wd.n.Expire()
	wd.chain.mu.Lock()
	wd.chain.delay = 0
	wd.chain.mu.Unlock()
	if r := wd.hello(wgKey(t), tk2); r.OK {
		t.Fatal("a paid ticket was kept past its hour")
	}
}

func TestExpiredIssuerKey(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	wd.chain.keys[keyID] = IssuerKey{Pubkey: wd.w.pk, RedeemEnd: wd.now.Add(30 * time.Minute).Unix()}
	if r := wd.hello(wgKey(t), wd.w.ticket(t, nodeID)); r.OK {
		t.Fatal("accepted a ticket that may never be redeemed")
	}
	wd2 := newWorld(t, "10.77.0.0/24")
	wd2.chain.expired = true
	if r := wd2.hello(wgKey(t), wd2.w.ticket(t, nodeID)); r.OK {
		t.Fatal("served when the chain said the key had expired")
	}
}

func TestRenewalAndExpiry(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	pub := wgKey(t)
	first := wd.hello(pub, wd.w.ticket(t, nodeID))
	wd.advance(40 * time.Minute)
	second := wd.hello(pub, wd.w.ticket(t, nodeID))
	if !second.OK || second.TunnelIP != first.TunnelIP || second.Expires != first.Expires+3600 {
		t.Fatalf("renewal %+v after %+v", second, first)
	}
	// Two tickets at once buy two hours.
	r := wd.hello(wgKey(t), wd.w.ticket(t, nodeID), wd.w.ticket(t, nodeID))
	if !r.OK || r.Expires != wd.now.Add(2*time.Hour).Unix() {
		t.Fatalf("two tickets %+v", r)
	}
	wd.advance(79 * time.Minute)
	if wd.n.Expire() != 0 {
		t.Fatal("ended early")
	}
	wd.advance(time.Minute)
	if wd.n.Expire() != 1 || wd.tun.count() != 1 {
		t.Fatalf("expiry: %d peers left", wd.tun.count())
	}
	// Its address is free again, and an ended session can be reopened.
	if r := wd.hello(pub, wd.w.ticket(t, nodeID)); !r.OK || r.TunnelIP != first.TunnelIP {
		t.Fatalf("reopen %+v", r)
	}
}

func TestFullNode(t *testing.T) {
	// A /29 holds 8 addresses: network, node, five clients, broadcast.
	wd := newWorld(t, "10.77.0.0/29", func(c *Config) { c.PaidKeep = 2 * time.Hour })
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		r := wd.hello(wgKey(t), wd.w.ticket(t, nodeID))
		if !r.OK || seen[r.TunnelIP] {
			t.Fatalf("client %d: %+v", i, r)
		}
		seen[r.TunnelIP] = true
	}
	for _, bad := range []string{"10.77.0.0", "10.77.0.1", "10.77.0.7"} {
		if seen[bad] {
			t.Fatalf("handed out %s", bad)
		}
	}
	// A full node keeps the ticket it was paid, for a later try.
	tk := wd.w.ticket(t, nodeID)
	if r := wd.hello(wgKey(t), tk); r.OK {
		t.Fatal("over capacity")
	}
	wd.advance(time.Hour)
	wd.n.Expire()
	if r := wd.hello(wgKey(t), tk); !r.OK {
		t.Fatalf("paid ticket lost when the node was full: %s", r.Error)
	}
}

func TestTunnelFailureKeepsTicket(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	wd.tun.fail = true
	tk := wd.w.ticket(t, nodeID)
	if r := wd.hello(wgKey(t), tk); r.OK {
		t.Fatal("accepted with a broken tunnel")
	}
	wd.tun.fail = false
	if r := wd.hello(wgKey(t), tk); !r.OK {
		t.Fatalf("paid ticket lost after a failed open: %s", r.Error)
	}
	if wd.chain.redeemedTxs() != 1 {
		t.Fatal("paid twice")
	}
}

// Many clients racing with one ticket: exactly one wins and it is redeemed
// once.
func TestRaceOnOneTicket(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	tk := wd.w.ticket(t, nodeID)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 20; i++ {
		pub := wgKey(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := wd.hello(pub, tk); r.OK {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 || wd.tun.count() != 1 || wd.chain.redeemedTxs() != 1 {
		t.Fatalf("%d wins, %d peers, %d redemptions", wins, wd.tun.count(), wd.chain.redeemedTxs())
	}
}

func TestNewChecksConfig(t *testing.T) {
	good := Config{Subnet: netip.MustParsePrefix("10.77.0.0/24"), SessionLength: time.Hour, MaxBatch: 1}
	if _, err := New(good, nil, nil); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]Config{
		"ipv6":       {Subnet: netip.MustParsePrefix("fd00::/64"), SessionLength: time.Hour, MaxBatch: 1},
		"too small":  {Subnet: netip.MustParsePrefix("10.77.0.0/31"), SessionLength: time.Hour, MaxBatch: 1},
		"no session": {Subnet: good.Subnet, MaxBatch: 1},
		"no batch":   {Subnet: good.Subnet, SessionLength: time.Hour},
	} {
		if _, err := New(c, nil, nil); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func (wd *world) prepay(pub string, tickets ...string) control.Reply {
	return wd.n.HandleHello(control.Hello{Version: control.Version, KeyID: keyID, Tickets: tickets, WGPubkey: pub, Prepay: true})
}

// Paying ahead: the ticket is redeemed at once, no session opens, and a
// later Hello with no tickets opens the session from the credit without
// touching the chain.
func TestPrepaidCredit(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	pub := wgKey(t)
	r := wd.prepay(pub, wd.w.ticket(t, nodeID), wd.w.ticket(t, nodeID))
	if !r.OK || r.TunnelIP != "" || r.Expires != wd.now.Add(24*time.Hour).Unix() {
		t.Fatalf("prepay %+v", r)
	}
	if wd.tun.count() != 0 {
		t.Fatal("a prepayment opened a session")
	}
	if wd.chain.redeemedTxs() != 1 {
		t.Fatal("prepaid tickets were not redeemed at once")
	}
	if s := wd.n.Stats(); s.Credits != 1 || s.Prepaid != 2 || s.Open != 0 {
		t.Fatalf("stats %+v", s)
	}

	// Hours later the client opens the session from its credit.
	wd.advance(5 * time.Hour)
	r = wd.hello(pub)
	if !r.OK || r.TunnelIP != "10.77.0.2" || r.Expires != wd.now.Add(2*time.Hour).Unix() {
		t.Fatalf("use credit %+v", r)
	}
	if wd.chain.redeemedTxs() != 1 {
		t.Fatal("using a credit touched the chain")
	}
	if s := wd.n.Stats(); s.Credits != 0 || s.CreditsUsed != 1 || s.Open != 1 {
		t.Fatalf("stats %+v", s)
	}
	// A credit is used once.
	if r := wd.hello(pub); r.OK {
		t.Fatal("a credit was used twice")
	}
}

func TestCreditRefusals(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24")
	if r := wd.hello(wgKey(t)); r.OK {
		t.Fatal("opened a session with no credit")
	}
	if r := wd.prepay(wgKey(t)); r.OK {
		t.Fatal("a prepayment without tickets was accepted")
	}
	// A credit lapses after a day.
	pub := wgKey(t)
	wd.prepay(pub, wd.w.ticket(t, nodeID))
	wd.advance(24 * time.Hour)
	if r := wd.hello(pub); r.OK {
		t.Fatal("a lapsed credit was used")
	}
	// Bad tickets buy no credit.
	pub2 := wgKey(t)
	used := wd.w.ticket(t, nodeID)
	wd.hello(wgKey(t), used)
	if r := wd.prepay(pub2, used); r.OK {
		t.Fatal("a spent ticket bought credit")
	}
	if r := wd.prepay(pub2, wd.w.ticket(t, nodeID+1)); r.OK {
		t.Fatal("another node's ticket bought credit")
	}
	if r := wd.hello(pub2); r.OK {
		t.Fatal("a refused prepayment left a credit")
	}
}

func TestCreditsAddUpAndPrune(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/29")
	pub := wgKey(t)
	wd.prepay(pub, wd.w.ticket(t, nodeID))
	wd.prepay(pub, wd.w.ticket(t, nodeID))
	if r := wd.hello(pub); !r.OK || r.Expires != wd.now.Add(2*time.Hour).Unix() {
		t.Fatalf("two prepayments %+v", r)
	}
	// A credit used for an open session extends it.
	wd.prepay(pub, wd.w.ticket(t, nodeID))
	if r := wd.hello(pub); !r.OK || r.Expires != wd.now.Add(3*time.Hour).Unix() {
		t.Fatalf("extend from credit %+v", r)
	}
	// An unused credit is forgotten after a day.
	wd.prepay(wgKey(t), wd.w.ticket(t, nodeID))
	if wd.n.Stats().Credits != 1 {
		t.Fatal("credit not held")
	}
	wd.advance(24 * time.Hour)
	wd.n.Expire()
	if wd.n.Stats().Credits != 0 {
		t.Fatal("lapsed credit kept")
	}
}

// A full node keeps the credit for a later try.
func TestCreditKeptWhenFull(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/30")
	// A /30 holds one client.
	if r := wd.hello(wgKey(t), wd.w.ticket(t, nodeID)); !r.OK {
		t.Fatal(r.Error)
	}
	pub := wgKey(t)
	wd.prepay(pub, wd.w.ticket(t, nodeID))
	if r := wd.hello(pub); r.OK {
		t.Fatal("over capacity")
	}
	wd.advance(time.Hour)
	wd.n.Expire()
	if r := wd.hello(pub); !r.OK {
		t.Fatalf("credit lost when the node was full: %s", r.Error)
	}
}

func TestReplyOffersDNS(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/24", func(c *Config) { c.DNS = netip.MustParseAddr("10.77.0.1") })
	if r := wd.hello(wgKey(t), wd.w.ticket(t, nodeID)); !r.OK || r.DNS != "10.77.0.1" {
		t.Fatalf("%+v", r)
	}
	wd2 := newWorld(t, "10.77.0.0/24")
	if r := wd2.hello(wgKey(t), wd2.w.ticket(t, nodeID)); !r.OK || r.DNS != "" {
		t.Fatalf("%+v", r)
	}
}

func TestClose(t *testing.T) {
	wd := newWorld(t, "10.77.0.0/30")
	pub := wgKey(t)
	if r := wd.hello(pub, wd.w.ticket(t, nodeID)); !r.OK {
		t.Fatal(r.Error)
	}
	// A /30 holds one client; closing frees its place at once.
	if r := wd.hello(wgKey(t), wd.w.ticket(t, nodeID)); r.OK {
		t.Fatal("over capacity")
	}
	close := func(p string) control.Reply {
		return wd.n.HandleHello(control.Hello{Version: control.Version, KeyID: keyID, WGPubkey: p, Close: true})
	}
	if r := close(pub); !r.OK {
		t.Fatal(r.Error)
	}
	if wd.tun.count() != 0 || wd.n.Stats().Open != 0 || wd.n.Stats().Closed != 1 {
		t.Fatalf("session not ended: %+v", wd.n.Stats())
	}
	// Closing a key with no session says the same, and changes nothing.
	if r := close(wgKey(t)); !r.OK || wd.n.Stats().Closed != 1 {
		t.Fatal("close of an unknown key")
	}
	pub2 := wgKey(t)
	if r := wd.hello(pub2, wd.w.ticket(t, nodeID)); !r.OK {
		t.Fatalf("place not freed: %s", r.Error)
	}
}

type fakeRelay struct {
	requested []string
	state     uint32
}

func (f *fakeRelay) RequestBridge(_ context.Context, _ uint64, ticket, rk string) error {
	f.requested = append(f.requested, ticket+"|"+rk)
	return nil
}
func (f *fakeRelay) BridgeAnswer(context.Context, string) (string, uint32, error) {
	return "c2VhbGVk", f.state, nil
}
func (f *fakeRelay) BridgeFingerprint(context.Context, uint64) (string, error) { return "ab", nil }

func TestBridgeOps(t *testing.T) {
	relay := &fakeRelay{state: 2}
	wd := newWorld(t, "10.77.0.0/24", func(c *Config) { c.Bridges = relay })
	rk := base64.StdEncoding.EncodeToString(make([]byte, 32))
	r := wd.n.HandleHello(control.Hello{Version: control.Version, Op: control.OpBridgeRequest, KeyID: keyID, Tickets: []string{"t"}, RequestKey: rk})
	if !r.OK || len(relay.requested) != 1 {
		t.Fatalf("%+v", r)
	}
	r = wd.n.HandleHello(control.Hello{Version: control.Version, Op: control.OpBridgeAnswer, RequestKey: rk})
	if !r.OK || r.Sealed != "c2VhbGVk" || r.State != 2 {
		t.Fatalf("%+v", r)
	}
	r = wd.n.HandleHello(control.Hello{Version: control.Version, Op: control.OpBridgeFingerprint, NodeID: 3})
	if !r.OK || r.Commitment != "ab" {
		t.Fatalf("%+v", r)
	}
	// No session was opened, and the node keeps no record of any of it.
	if wd.n.Stats().Open != 0 {
		t.Fatal("a bridge request opened a session")
	}
	// A node that does not relay says so.
	wd2 := newWorld(t, "10.77.0.0/24")
	if r := wd2.n.HandleHello(control.Hello{Version: control.Version, Op: control.OpBridgeFingerprint}); r.OK {
		t.Fatal("relayed with no relay")
	}
}
