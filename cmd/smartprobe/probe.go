package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mspvantage/smartnodes/vpn/chain"
	"github.com/mspvantage/smartnodes/vpn/probe"
)

func hexBytes(s string) ([]byte, error) { return hex.DecodeString(s) }

// pickPartner chooses a random active node of the other tier, on another
// address, to make up the second hop.
func pickPartner(target chain.NodeInfo, nodes []chain.NodeInfo) (chain.NodeInfo, bool) {
	want := uint64(chain.TierExit)
	if target.Tier == chain.TierExit {
		want = chain.TierEntry
	}
	host := func(ep string) string {
		h, _, _ := strings.Cut(ep, ":")
		return h
	}
	var options []chain.NodeInfo
	for _, n := range nodes {
		// Bridges are never used: their addresses are not on the chain.
		if n.Tier == want && n.Status == chain.StatusActive && n.Commitment == "" && n.Endpoint != "" &&
			host(n.Endpoint) != host(target.Endpoint) && n.Operator != target.Operator {
			options = append(options, n)
		}
	}
	if len(options) == 0 {
		return chain.NodeInfo{}, false
	}
	return options[uniform(time.Duration(len(options)-1))], true
}

// hopReached reads how far smartvpn connect got. ok is false when the
// output does not show a fault at either node, so the failure was this
// machine's own and says nothing about the nodes.
func hopReached(out string, entryID, exitID uint64) (probe.Hop, bool) {
	fault := func(id uint64) bool {
		for _, f := range []string{"cannot reach node %d:", "node %d refused:", "node %d did not answer properly:"} {
			if strings.Contains(out, fmt.Sprintf(f, id)) {
				return true
			}
		}
		return false
	}
	entryUp := strings.Contains(out, fmt.Sprintf("entry node %d: tunnel address", entryID))
	switch {
	case strings.Contains(out, "connected through two hops"):
		return probe.BothHops, true
	case entryUp && fault(exitID):
		return probe.EntryOnly, true
	case !entryUp && fault(entryID):
		return probe.NoHop, true
	}
	return 0, false
}

// probeNode probes one node and decides the report. ok is false if no
// report should be sent.
func (p *prober) probeNode(ctx context.Context, round uint64, target chain.NodeInfo, nodes []chain.NodeInfo, known []probe.Known) (probe.Report, bool) {
	partner, ok := pickPartner(target, nodes)
	if !ok {
		log.Printf("round %d: no node of the other tier to probe node %d through; skipping it", round, target.ID)
		return probe.Report{}, false
	}
	entry, exit, role := target, partner, probe.Entry
	if target.Tier == chain.TierExit {
		entry, exit, role = partner, target, probe.Exit
	}
	entryTicket, err := p.wallet.Spend(entry.ID)
	if err != nil {
		log.Printf("round %d: %v; cannot probe", round, err)
		return probe.Report{}, false
	}
	exitTicket, err := p.wallet.Spend(exit.ID)
	if err != nil {
		log.Printf("round %d: %v; cannot probe", round, err)
		return probe.Report{}, false
	}

	ns, err := p.net.create()
	if err != nil {
		log.Printf("round %d: could not make a network space: %v", round, err)
		return probe.Report{}, false
	}
	defer p.net.destroy(ns)
	state := "/run/smartprobe/" + ns + ".json"
	env := append(os.Environ(), "SMARTVPN_STATE="+state)

	// An entry that offers a disguise is probed through it half the time,
	// so it cannot advertise a way in that does not work.
	transport := []string{"-transport", "wireguard"}
	via := "WireGuard"
	if entry.Reality != nil && uniform(1) == 1 {
		transport = []string{"-transport", "reality", "-entry-reality", entry.Reality.Compact(), "-xray", p.xray}
		via = "the disguise"
	}

	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ip", "netns", "exec", ns, p.smartvpn, "connect",
		"-chain-id", p.chainID, "-key-id", strconv.FormatUint(p.wallet.KeyID(), 10),
		"-entry-id", strconv.FormatUint(entry.ID, 10), "-entry-key", entry.Pubkey, "-entry-endpoint", entry.Endpoint, "-entry-ticket", entryTicket,
		"-exit-id", strconv.FormatUint(exit.ID, 10), "-exit-key", exit.Pubkey, "-exit-endpoint", exit.Endpoint, "-exit-ticket", exitTicket,
		"-wait", p.wait.String())
	cmd.Args = append(cmd.Args, transport...)
	cmd.Env = env
	out, _ := cmd.CombinedOutput()
	hop, known2 := hopReached(string(out), entry.ID, exit.ID)
	if !known2 {
		log.Printf("round %d: probing node %d failed on this machine's side; no report: %s", round, target.ID, lastLine(out))
		p.disconnect(ns, env)
		return probe.Report{}, false
	}

	m := probe.Measurement{Reached: hop}
	if hop == probe.BothHops {
		args := []string{"netns", "exec", ns, p.self, "measure", "-speed-url", p.speedURL}
		for _, k := range known {
			args = append(args, "-known", k.URL+"="+k.SHA256)
		}
		mctx, mcancel := context.WithTimeout(ctx, 3*time.Minute)
		raw, err := exec.CommandContext(mctx, "ip", args...).Output()
		mcancel()
		if err == nil {
			_ = json.Unmarshal(raw, &m)
		}
		m.Reached = probe.BothHops
	}
	p.disconnect(ns, env)

	result, band, ok := probe.Verdict(role, m, p.minMbps[target.Tier])
	partnerName := map[probe.Role]string{probe.Entry: "exit", probe.Exit: "entry"}[role]
	if !ok {
		log.Printf("round %d: node %d through %s %d: the %s failed, not the node probed; no report", round, target.ID, partnerName, partner.ID, partnerName)
		return probe.Report{}, false
	}
	salt, err := probe.NewSalt()
	if err != nil {
		return probe.Report{}, false
	}
	log.Printf("round %d: node %d (%s) through %s %d, entry reached by %s: %s, %.0f Mbps, content %s, name lookups %s",
		round, target.ID, map[probe.Role]string{probe.Entry: "entry", probe.Exit: "exit"}[role], partnerName, partner.ID, via,
		resultName(result), m.Mbps, okWord(m.IntegrityOK || hop != probe.BothHops), okWord(m.DNSOK || hop != probe.BothHops))
	return probe.Report{Round: round, NodeID: target.ID, Result: result, Speed: band, Salt: salt}, true
}

func okWord(b bool) string {
	if b {
		return "unaltered"
	}
	return "ALTERED"
}

func (p *prober) disconnect(ns string, env []string) {
	cmd := exec.Command("ip", "netns", "exec", ns, p.smartvpn, "down")
	cmd.Env = env
	_ = cmd.Run()
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return lines[len(lines)-1]
}
