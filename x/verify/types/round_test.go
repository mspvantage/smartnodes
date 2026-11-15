package types

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func seedOf(b byte) []byte { return bytes.Repeat([]byte{b}, HashLength) }

func TestNextSeed(t *testing.T) {
	a := NextSeed(nil, 1, []byte("block-a"))
	if len(a) != HashLength {
		t.Fatalf("seed length %d", len(a))
	}
	if !bytes.Equal(a, NextSeed(nil, 1, []byte("block-a"))) {
		t.Fatal("seed is not deterministic")
	}
	for name, other := range map[string][]byte{
		"different block": NextSeed(nil, 1, []byte("block-b")),
		"different round": NextSeed(nil, 2, []byte("block-a")),
		"different prev":  NextSeed(seedOf(1), 1, []byte("block-a")),
	} {
		if bytes.Equal(a, other) {
			t.Fatalf("%s gave the same seed", name)
		}
	}
	// The length prefix keeps (prev, hash) pairs from colliding.
	if bytes.Equal(NextSeed([]byte("ab"), 1, []byte("c")), NextSeed([]byte("a"), 1, []byte("bc"))) {
		t.Fatal("seed inputs are ambiguous")
	}
}

func TestSelected(t *testing.T) {
	seed := seedOf(7)
	for n := uint64(0); n < 200; n++ {
		if !Selected(seed, n, BpsDenominator) {
			t.Fatal("100% sampling skipped a node")
		}
		if Selected(seed, n, 0) {
			t.Fatal("0% sampling selected a node")
		}
		if Selected(seed, n, 2500) != Selected(seed, n, 2500) {
			t.Fatal("selection is not deterministic")
		}
	}
	// One round in four: over many nodes the share should be near 25%.
	hits := 0
	const total = 20000
	for n := uint64(0); n < total; n++ {
		if Selected(seed, n, 2500) {
			hits++
		}
	}
	if hits < total*23/100 || hits > total*27/100 {
		t.Fatalf("25%% sampling selected %d of %d", hits, total)
	}
	// A different seed selects a different set.
	same := 0
	for n := uint64(0); n < 2000; n++ {
		if Selected(seed, n, 2500) == Selected(seedOf(8), n, 2500) {
			same++
		}
	}
	if same == 2000 {
		t.Fatal("selection does not depend on the seed")
	}
}

func TestAssignedIndices(t *testing.T) {
	seed := seedOf(3)

	if got := AssignedIndices(seed, 1, 0, 3); got != nil {
		t.Fatalf("no probers: got %v", got)
	}
	if got := AssignedIndices(seed, 1, 5, 0); got != nil {
		t.Fatalf("zero per node: got %v", got)
	}
	// Fewer probers than slots: everyone is assigned.
	for count := uint64(1); count <= 3; count++ {
		got := AssignedIndices(seed, 9, count, 3)
		if uint64(len(got)) != count {
			t.Fatalf("count %d: got %v", count, got)
		}
		for i, v := range got {
			if v != uint64(i) {
				t.Fatalf("count %d: got %v", count, got)
			}
		}
	}
	// More probers than slots: exactly perNode distinct, in-range positions.
	for _, count := range []uint64{4, 5, 16, 1000, 1 << 40} {
		for node := uint64(0); node < 300; node++ {
			got := AssignedIndices(seed, node, count, 3)
			if len(got) != 3 {
				t.Fatalf("count %d node %d: got %v", count, node, got)
			}
			if got[0] == got[1] || got[0] == got[2] || got[1] == got[2] {
				t.Fatalf("count %d node %d: duplicates in %v", count, node, got)
			}
			for _, v := range got {
				if v >= count {
					t.Fatalf("count %d node %d: out of range %v", count, node, got)
				}
				if !IsAssigned(seed, node, count, 3, v) {
					t.Fatal("IsAssigned disagrees with AssignedIndices")
				}
			}
			again := AssignedIndices(seed, node, count, 3)
			if got[0] != again[0] || got[1] != again[1] || got[2] != again[2] {
				t.Fatal("assignment is not deterministic")
			}
		}
	}
	// perNode at its maximum with one more prober than slots.
	got := AssignedIndices(seed, 1, MaxProbersPerNode+1, MaxProbersPerNode)
	if len(got) != MaxProbersPerNode {
		t.Fatalf("max per node: got %d positions", len(got))
	}
}

// Every prober should get a fair share of the work.
func TestAssignmentIsEven(t *testing.T) {
	const probers, nodes = 10, 20000
	counts := make([]int, probers)
	for node := uint64(0); node < nodes; node++ {
		for _, idx := range AssignedIndices(seedOf(5), node, probers, 3) {
			counts[idx]++
		}
	}
	want := nodes * 3 / probers
	for i, c := range counts {
		if c < want*95/100 || c > want*105/100 {
			t.Fatalf("prober %d got %d assignments, want about %d", i, c, want)
		}
	}
}

// A new seed must reshuffle who probes a node.
func TestAssignmentChangesWithSeed(t *testing.T) {
	changed := 0
	for node := uint64(0); node < 200; node++ {
		a := AssignedIndices(seedOf(1), node, 50, 3)
		b := AssignedIndices(seedOf(2), node, 50, 3)
		if a[0] != b[0] || a[1] != b[1] || a[2] != b[2] {
			changed++
		}
	}
	if changed < 190 {
		t.Fatalf("only %d of 200 assignments changed with the seed", changed)
	}
}

func TestCommitHash(t *testing.T) {
	salt := bytes.Repeat([]byte{9}, 16)
	base := CommitHash("chain-a", 5, 7, 2, ResultPass, 3, salt)
	if len(base) != HashLength {
		t.Fatalf("hash length %d", len(base))
	}
	if !bytes.Equal(base, CommitHash("chain-a", 5, 7, 2, ResultPass, 3, salt)) {
		t.Fatal("hash is not deterministic")
	}
	for name, other := range map[string][]byte{
		"chain":  CommitHash("chain-b", 5, 7, 2, ResultPass, 3, salt),
		"round":  CommitHash("chain-a", 6, 7, 2, ResultPass, 3, salt),
		"node":   CommitHash("chain-a", 5, 8, 2, ResultPass, 3, salt),
		"prober": CommitHash("chain-a", 5, 7, 3, ResultPass, 3, salt),
		"result": CommitHash("chain-a", 5, 7, 2, ResultFail, 3, salt),
		"speed":  CommitHash("chain-a", 5, 7, 2, ResultPass, 4, salt),
		"salt":   CommitHash("chain-a", 5, 7, 2, ResultPass, 3, bytes.Repeat([]byte{8}, 16)),
	} {
		if bytes.Equal(base, other) {
			t.Fatalf("changing the %s did not change the hash", name)
		}
	}
}

func TestDecodeHashAndSalt(t *testing.T) {
	good := hex.EncodeToString(seedOf(0xab))
	if _, err := DecodeHash(good); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]string{
		"empty":     "",
		"short":     good[:62],
		"long":      good + "00",
		"uppercase": "AB" + good[2:],
		"not hex":   "zz" + good[2:],
	} {
		if _, err := DecodeHash(bad); err == nil {
			t.Fatalf("hash %s was accepted", name)
		}
	}

	if _, err := DecodeSalt(hex.EncodeToString(make([]byte, MinSaltLength))); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSalt(hex.EncodeToString(make([]byte, MaxSaltLength))); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]string{
		"too short": hex.EncodeToString(make([]byte, MinSaltLength-1)),
		"too long":  hex.EncodeToString(make([]byte, MaxSaltLength+1)),
		"uppercase": "AA" + hex.EncodeToString(make([]byte, MinSaltLength-1)),
		"odd":       hex.EncodeToString(make([]byte, MinSaltLength)) + "0",
	} {
		if _, err := DecodeSalt(bad); err == nil {
			t.Fatalf("salt %s was accepted", name)
		}
	}
}

func TestTally(t *testing.T) {
	cases := []struct {
		name               string
		pass, fail, tamper uint64
		perNode            uint64
		outcome            string
		proven             bool
	}{
		{"three passes", 3, 0, 0, 3, OutcomePass, false},
		{"two passes one fail", 2, 1, 0, 3, OutcomePass, false},
		{"two passes, one silent", 2, 0, 0, 3, OutcomePass, false},
		{"two fails one pass", 1, 2, 0, 3, OutcomeFail, false},
		{"fail and tamper together", 1, 1, 1, 3, OutcomeFail, false},
		{"two tamper one pass", 1, 0, 2, 3, OutcomeFail, false},
		{"three tamper", 0, 0, 3, 3, OutcomeFail, true},
		{"one of each side", 1, 1, 0, 3, OutcomeUnverified, false},
		{"single pass", 1, 0, 0, 3, OutcomeUnverified, false},
		{"single fail cannot fail a node", 0, 1, 0, 3, OutcomeUnverified, false},
		{"single tamper cannot fail a node", 0, 0, 1, 3, OutcomeUnverified, false},
		{"nothing opened", 0, 0, 0, 3, OutcomeUnverified, false},
		{"five probers, three pass", 3, 2, 0, 5, OutcomePass, false},
		{"five probers, two pass", 2, 2, 0, 5, OutcomeUnverified, false},
		{"one prober configured", 1, 0, 0, 1, OutcomePass, false},
	}
	for _, c := range cases {
		outcome, proven := Tally(c.pass, c.fail, c.tamper, c.perNode)
		if outcome != c.outcome || proven != c.proven {
			t.Errorf("%s: got %s/%v, want %s/%v", c.name, outcome, proven, c.outcome, c.proven)
		}
	}
	if Majority(3) != 2 || Majority(5) != 3 || Majority(1) != 1 || Majority(4) != 3 {
		t.Fatal("majority is wrong")
	}
}
