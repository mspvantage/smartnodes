package main

import (
	"testing"

	"github.com/mspvantage/smartnodes/vpn/chain"
	"github.com/mspvantage/smartnodes/vpn/probe"
)

func TestHopReached(t *testing.T) {
	cases := []struct {
		out string
		hop probe.Hop
		ok  bool
	}{
		{"12:00:00 entry node 0: tunnel address 10.77.0.2\n12:00:09 connected through two hops; ...", probe.BothHops, true},
		{"12:00:00 entry node 0: tunnel address 10.77.0.2\nsmartvpn: cannot reach node 1: i/o timeout", probe.EntryOnly, true},
		{"12:00:00 entry node 0: tunnel address 10.77.0.2\nsmartvpn: node 1 refused: this node is full", probe.EntryOnly, true},
		{"smartvpn: cannot reach node 0: connection refused", probe.NoHop, true},
		{"smartvpn: node 0 did not answer properly: control: timeout", probe.NoHop, true},
		// Not the nodes' fault: this machine could not set up its side.
		{"smartvpn: kill switch: iptables-restore: not found", 0, false},
		// Node 10 is not node 1.
		{"12:00:00 entry node 0: tunnel address 10.77.0.2\nsmartvpn: node 10 refused: x", 0, false},
	}
	for _, c := range cases {
		hop, ok := hopReached(c.out, 0, 1)
		if hop != c.hop || ok != c.ok {
			t.Errorf("%q: got %v %v, want %v %v", c.out, hop, ok, c.hop, c.ok)
		}
	}
}

func TestPickPartner(t *testing.T) {
	nodes := []chain.NodeInfo{
		{ID: 0, Tier: chain.TierEntry, Status: 1, Endpoint: "10.0.0.1:1", Operator: "a"},
		{ID: 1, Tier: chain.TierExit, Status: 1, Endpoint: "10.0.0.2:1", Operator: "b"},
		{ID: 2, Tier: chain.TierExit, Status: 2, Endpoint: "10.0.0.3:1", Operator: "c"}, // leaving
		{ID: 3, Tier: chain.TierExit, Status: 1, Endpoint: "10.0.0.1:2", Operator: "d"}, // same machine as 0
		{ID: 4, Tier: chain.TierExit, Status: 1, Endpoint: "10.0.0.4:1", Operator: "a"}, // same operator as 0
	}
	for i := 0; i < 50; i++ {
		p, ok := pickPartner(nodes[0], nodes)
		if !ok || p.ID != 1 {
			t.Fatalf("partner %+v %v", p, ok)
		}
	}
	p, ok := pickPartner(nodes[1], nodes)
	if !ok || p.ID != 0 {
		t.Fatalf("partner for an exit %+v %v", p, ok)
	}
	if _, ok := pickPartner(nodes[0], nodes[:1]); ok {
		t.Fatal("found a partner where there is none")
	}
}
