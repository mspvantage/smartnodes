package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mspvantage/smartnodes/vpn/chain"
	"github.com/mspvantage/smartnodes/vpn/probe"
	"github.com/mspvantage/smartnodes/vpn/wallet"
)

const (
	defaultSpeedURL = "https://speed.cloudflare.com/__down?bytes=25000000"
	// The default known files are ordinary public files that never change.
	defaultKnown1 = "https://raw.githubusercontent.com/golang/go/go1.22.0/LICENSE"
	defaultKnown2 = "https://www.rfc-editor.org/rfc/rfc8446.txt"
)

type urlList []string

func (u *urlList) String() string     { return strings.Join(*u, ",") }
func (u *urlList) Set(v string) error { *u = append(*u, v); return nil }

// prober holds what one running prober needs.
type prober struct {
	cli      *chain.CLI
	wallet   *wallet.Wallet
	store    *probe.Store
	net      *netPlan
	self     string // path to this program
	smartvpn string
	xray     string
	id       uint64
	chainID  string

	wait      time.Duration
	minMbps   map[uint64]float64
	speedURL  string
	knownURLs []string

	// sendMu keeps this account's transactions in order.
	sendMu sync.Mutex
	// blockTime is the measured time between blocks.
	blockTime time.Duration
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	from := fs.String("from", "", "the prober's key")
	backend := fs.String("keyring-backend", "test", "keyring backend of that key")
	home := fs.String("home", "", "smartnodesd home folder holding that key")
	rpc := fs.String("rpc", "tcp://localhost:26657", "chain node to talk to")
	chainID := fs.String("chain-id", "smartnodes", "chain id")
	gasPrices := fs.String("gas-prices", "0.025usnode", "gas price for reports")
	binary := fs.String("smartnodesd", "smartnodesd", "path to smartnodesd")
	smartvpn := fs.String("smartvpn", "smartvpn", "path to smartvpn")
	xray := fs.String("xray", "xray", "path to the Xray program, to probe disguised entry nodes")
	walletPath := fs.String("wallet", "", "ticket wallet that pays for probes")
	stateDir := fs.String("state-dir", "/var/lib/smartprobe", "folder for reports waiting to be opened")
	uplink := fs.String("uplink", "", "this machine's internet interface (default: the one with the default route)")
	wait := fs.Duration("wait", 30*time.Second, "longest random wait between paying an exit and using it")
	speedURL := fs.String("speed-url", defaultSpeedURL, "file to download to measure speed")
	var known urlList
	fs.Var(&known, "known-url", "an ordinary public file that never changes, to check traffic is unaltered; repeatable")
	_ = fs.Parse(args)

	if os.Geteuid() != 0 {
		return errors.New("must run as root, to create network spaces and WireGuard interfaces")
	}
	if *from == "" || *home == "" || *walletPath == "" {
		return errors.New("-from, -home and -wallet are required")
	}
	if len(known) == 0 {
		known = urlList{defaultKnown1, defaultKnown2}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	vpnPath, err := exec.LookPath(*smartvpn)
	if err != nil {
		return fmt.Errorf("smartvpn: %w", err)
	}
	w, err := wallet.Open(*walletPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		return err
	}
	store, err := probe.OpenStore(filepath.Join(*stateDir, "reports.json"))
	if err != nil {
		return err
	}
	cli := &chain.CLI{Run: chain.Exec(*binary), RPC: *rpc, ChainID: *chainID, From: *from,
		KeyringBackend: *backend, Home: *home, GasPrices: *gasPrices}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	owner, err := cli.SignerAddress(ctx)
	if err != nil {
		return err
	}
	info, err := cli.ProberByOwner(ctx, owner)
	if err != nil {
		return err
	}
	plan, err := newNetPlan(info.ID, *uplink)
	if err != nil {
		return err
	}
	if err := plan.setUpHost(); err != nil {
		return err
	}
	defer plan.tearDownHost()

	p := &prober{cli: cli, wallet: w, store: store, net: plan, self: self, smartvpn: vpnPath, xray: *xray, id: info.ID,
		chainID: *chainID, wait: *wait, speedURL: *speedURL, knownURLs: known,
		minMbps: map[uint64]float64{}}
	log.Printf("prober %d (%s) started; %d tickets in the wallet", p.id, owner, w.Left())
	return p.loop(ctx)
}

// loop watches for new rounds and works each one.
func (p *prober) loop(ctx context.Context) error {
	var seen uint64
	var t0 time.Time
	var h0 int64
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		if h, err := p.cli.Height(ctx); err == nil {
			now := time.Now()
			if t0.IsZero() {
				t0, h0 = now, h
			} else if h > h0 && now.Sub(t0) > 5*time.Second {
				p.blockTime = now.Sub(t0) / time.Duration(h-h0)
			}
			r, err := p.cli.Round(ctx)
			if err == nil && r.Round > 0 && r.Round != seen && p.blockTime > 0 {
				seen = r.Round
				length := time.Duration(r.NextHeight-r.StartHeight) * p.blockTime
				left := time.Duration(r.NextHeight-h) * p.blockTime
				log.Printf("round %d: about %s long, %s left", r.Round, length.Round(time.Second), left.Round(time.Second))
				wg.Add(2)
				go func() { defer wg.Done(); p.openReports(ctx, r.Round, left) }()
				go func() { defer wg.Done(); p.probeRound(ctx, r.Round, left) }()
			}
		}
		select {
		case <-ctx.Done():
			log.Printf("stopping")
			return nil
		case <-time.After(3 * time.Second):
		}
	}
}

// uniform picks a time from 0 to max from the secure random source.
func uniform(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)+1))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// openReports opens last round's sealed reports, each at a random time in
// the first part of this round.
func (p *prober) openReports(ctx context.Context, round uint64, left time.Duration) {
	reports, err := p.store.ToOpen(round)
	if err != nil {
		log.Printf("round %d: reading saved reports: %v", round, err)
		return
	}
	window := left * 2 / 5
	for _, r := range reports {
		if !sleep(ctx, uniform(window/time.Duration(len(reports)))) {
			return
		}
		p.sendMu.Lock()
		err := p.cli.RevealReport(ctx, r.Round, r.NodeID, r.Result, r.Speed, r.Salt)
		p.sendMu.Unlock()
		if err != nil {
			log.Printf("round %d: opening the report on node %d failed: %v", round, r.NodeID, err)
			continue
		}
		_ = p.store.Remove(r.Round, r.NodeID)
		log.Printf("round %d: opened the report on node %d from round %d: %s", round, r.NodeID, r.Round, resultName(r.Result))
	}
}

func resultName(r uint64) string {
	switch r {
	case probe.ResultPass:
		return "pass"
	case probe.ResultFail:
		return "fail"
	case probe.ResultTamper:
		return "tamper"
	}
	return "unknown"
}

// probeRound probes this round's assigned nodes and seals the results.
func (p *prober) probeRound(ctx context.Context, round uint64, left time.Duration) {
	start := time.Now()
	probeBy := start.Add(left * 7 / 10)
	commitBy := start.Add(left * 85 / 100)

	info, err := p.cli.ProberByOwner(ctx, mustOwner(ctx, p.cli))
	if err != nil || (info.Status != chain.ProberStatusActive && info.Status != 3) {
		log.Printf("round %d: not in the active set yet; nothing to probe", round)
		return
	}
	// Every prober judges speed by the chain's settings.
	minEntry, minExit, err := p.cli.MinSpeeds(ctx)
	if err != nil {
		log.Printf("round %d: reading the speed settings: %v", round, err)
		return
	}
	p.minMbps = map[uint64]float64{chain.TierEntry: float64(minEntry), chain.TierExit: float64(minExit)}
	nodes, err := p.cli.ListNodes(ctx)
	if err != nil {
		log.Printf("round %d: reading the registry: %v", round, err)
		return
	}
	assignedRound, assigned, err := p.cli.ProberAssignments(ctx, p.id)
	if err != nil || assignedRound != round {
		log.Printf("round %d: reading assignments: %v", round, err)
		return
	}
	byID := map[uint64]chain.NodeInfo{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	var mine []chain.NodeInfo
	for _, id := range assigned {
		if n, ok := byID[id]; ok && n.Status == chain.StatusActive {
			mine = append(mine, n)
		}
	}
	log.Printf("round %d: assigned %d node(s)", round, len(mine))
	if len(mine) == 0 {
		return
	}

	// Learn what the known files look like, outside any tunnel.
	known := p.learnKnown(ctx)

	// In random order, each after a random pause.
	shuffle(mine)
	var reports []probe.Report
	for i, n := range mine {
		budget := time.Until(probeBy) / time.Duration(len(mine)-i)
		if budget < time.Minute {
			log.Printf("round %d: no time left to probe node %d", round, n.ID)
			continue
		}
		if !sleep(ctx, uniform(budget/4)) {
			return
		}
		r, ok := p.probeNode(ctx, round, n, nodes, known)
		if ok {
			reports = append(reports, r)
		}
	}
	if len(reports) == 0 {
		return
	}

	// Saved before sealing, so a restart can still open them.
	if err := p.store.Add(reports...); err != nil {
		log.Printf("round %d: saving reports: %v", round, err)
		return
	}
	if !sleep(ctx, uniform(time.Until(commitBy)/2)) {
		return
	}
	var ids []uint64
	var hashes []string
	for _, r := range p.store.Uncommitted(round) {
		salt, _ := hexBytes(r.Salt)
		ids = append(ids, r.NodeID)
		hashes = append(hashes, probe.Seal(p.chainID, r.Round, r.NodeID, p.id, r.Result, r.Speed, salt))
	}
	p.sendMu.Lock()
	err = p.cli.CommitReports(ctx, round, ids, hashes)
	p.sendMu.Unlock()
	if err != nil {
		log.Printf("round %d: sealing %d report(s) failed: %v", round, len(ids), err)
		return
	}
	_ = p.store.MarkCommitted(round)
	log.Printf("round %d: sealed %d report(s) on the chain", round, len(ids))
}

func mustOwner(ctx context.Context, c *chain.CLI) string {
	a, _ := c.SignerAddress(ctx)
	return a
}

// learnKnown fetches the known files outside the tunnels.
func (p *prober) learnKnown(ctx context.Context) []probe.Known {
	client := &http.Client{Timeout: 30 * time.Second}
	var out []probe.Known
	for _, u := range p.knownURLs {
		sum, _, err := probe.Fetch(ctx, client, u, 16<<20)
		if err != nil {
			log.Printf("could not fetch %s outside the tunnels; not checking it this round", u)
			continue
		}
		out = append(out, probe.Known{URL: u, SHA256: sum})
	}
	return out
}

func shuffle(n []chain.NodeInfo) {
	for i := len(n) - 1; i > 0; i-- {
		j := int(uniform(time.Duration(i)))
		n[i], n[j] = n[j], n[i]
	}
}
