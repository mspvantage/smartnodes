package types

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

// Prober statuses.
const (
	// ProberPending probers have posted a bond and join the active set when
	// the next round starts.
	ProberPending uint64 = 1
	// ProberActive probers are in the active set and can be assigned nodes.
	ProberActive uint64 = 2
	// ProberLeaving probers have asked to leave. They stay in the active set,
	// and stay assignable, until the next round starts.
	ProberLeaving uint64 = 3
	// ProberUnbonding probers have left the active set and are waiting out
	// the unbonding period. Their bond stays locked.
	ProberUnbonding uint64 = 4
)

// Report results. A sealed report that has not been opened has result 0.
const (
	ResultSealed uint64 = 0
	// ResultPass: the node was reachable, fast enough and honest.
	ResultPass uint64 = 1
	// ResultFail: the node was unreachable or too slow.
	ResultFail uint64 = 2
	// ResultTamper: the node altered traffic or lied about name lookups.
	ResultTamper uint64 = 3
)

// Tally outcomes for one node in one round.
const (
	OutcomeUnverified = "unverified"
	OutcomePass       = "pass"
	OutcomeFail       = "fail"
)

const (
	// HashLength is the size of a sealed report and of a round seed.
	HashLength = sha256.Size
	// MinSaltLength and MaxSaltLength bound the random salt, in bytes, that
	// hides a sealed result from guessing.
	MinSaltLength = 16
	MaxSaltLength = 64
	// MaxSpeedBand is the highest speed band a report can carry.
	MaxSpeedBand = 255
	// MaxProofLength bounds the proof-of-probe field, in characters. The
	// proof is not checked until stage 2 of the verification module.
	MaxProofLength = 8192
	// MaxCommitsPerMsg bounds the sealed reports in one message.
	MaxCommitsPerMsg = 100
	// MaxProbersPerNode bounds the ProbersPerNode parameter.
	MaxProbersPerNode = 15
	// BpsDenominator is 100% in basis points.
	BpsDenominator = 10_000
)

// Domain separation tags. Each hash in this module starts with its own tag,
// so a value computed for one purpose can never be replayed for another.
const (
	domainSeed   = "SMARTNODES-VERIFY-SEED-V1"
	domainSelect = "SMARTNODES-VERIFY-SELECT-V1"
	domainAssign = "SMARTNODES-VERIFY-ASSIGN-V1"
	domainCommit = "SMARTNODES-VERIFY-COMMIT-V1"
)

func be64(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}

// NextSeed derives the seed of a round from the previous round's seed, the
// round number and the hash of the block that starts the round.
//
// The block hash is only known once the block is proposed, so nobody can work
// out assignments for a round before it starts. The proposer of that one
// block has a small influence over it; with a known, bonded validator set
// this is accepted for now, and a threshold random beacon replaces it later.
func NextSeed(prevSeed []byte, round uint64, blockHash []byte) []byte {
	h := sha256.New()
	h.Write([]byte(domainSeed))
	h.Write(be64(uint64(len(prevSeed))))
	h.Write(prevSeed)
	h.Write(be64(round))
	h.Write(blockHash)
	return h.Sum(nil)
}

// Selected reports whether a node is probed in the round with this seed.
// sampleBps is the share of nodes probed per round, in basis points.
func Selected(seed []byte, nodeID uint64, sampleBps uint64) bool {
	if sampleBps >= BpsDenominator {
		return true
	}
	h := sha256.New()
	h.Write([]byte(domainSelect))
	h.Write(seed)
	h.Write(be64(nodeID))
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])%BpsDenominator < sampleBps
}

// AssignedIndices returns the positions, in the active prober list, of the
// probers assigned to a node for the round with this seed. The result has
// min(perNode, activeCount) distinct positions, in the order drawn.
//
// Anyone can compute this from public data, and it costs the same however
// many probers or nodes exist.
func AssignedIndices(seed []byte, nodeID uint64, activeCount uint64, perNode uint64) []uint64 {
	if activeCount == 0 || perNode == 0 {
		return nil
	}
	if activeCount <= perNode {
		out := make([]uint64, activeCount)
		for i := range out {
			out[i] = uint64(i)
		}
		return out
	}

	out := make([]uint64, 0, perNode)
	taken := func(idx uint64) bool {
		for _, v := range out {
			if v == idx {
				return true
			}
		}
		return false
	}
	// Draw until perNode distinct positions are found. Because activeCount is
	// greater than perNode, each draw is new with probability above
	// 1/(perNode+1), so the bound below is never reached in practice. It
	// exists so that the loop provably ends.
	maxDraws := perNode * 256
	for counter := uint64(0); uint64(len(out)) < perNode && counter < maxDraws; counter++ {
		h := sha256.New()
		h.Write([]byte(domainAssign))
		h.Write(seed)
		h.Write(be64(nodeID))
		h.Write(be64(counter))
		sum := h.Sum(nil)
		idx := binary.BigEndian.Uint64(sum[:8]) % activeCount
		if !taken(idx) {
			out = append(out, idx)
		}
	}
	// Fallback, unreachable in practice: fill with the lowest free positions.
	for idx := uint64(0); uint64(len(out)) < perNode && idx < activeCount; idx++ {
		if !taken(idx) {
			out = append(out, idx)
		}
	}
	return out
}

// IsAssigned reports whether the prober at position idx of the active list
// is assigned to the node.
func IsAssigned(seed []byte, nodeID uint64, activeCount uint64, perNode uint64, idx uint64) bool {
	for _, v := range AssignedIndices(seed, nodeID, activeCount, perNode) {
		if v == idx {
			return true
		}
	}
	return false
}

// CommitHash is the sealed form of a report. A prober posts it during the
// round and opens it in the next one by sending the same values.
//
// The hash covers the chain, round, node and prober, so a sealed report
// cannot be copied by another prober or reused in another round, and a salt,
// so the result cannot be found by trying the few possible values.
func CommitHash(chainID string, round, nodeID, proberID, result, speedBand uint64, salt []byte) []byte {
	h := sha256.New()
	h.Write([]byte(domainCommit))
	h.Write(be64(uint64(len(chainID))))
	h.Write([]byte(chainID))
	h.Write(be64(round))
	h.Write(be64(nodeID))
	h.Write(be64(proberID))
	h.Write(be64(result))
	h.Write(be64(speedBand))
	h.Write(be64(uint64(len(salt))))
	h.Write(salt)
	return h.Sum(nil)
}

// DecodeHash parses a sealed report: exactly 64 lowercase hex characters.
func DecodeHash(s string) ([]byte, error) {
	if len(s) != 2*HashLength {
		return nil, errors.New("want 64 hex characters")
	}
	return decodeLowerHex(s)
}

// DecodeSalt parses a salt: lowercase hex of 16 to 64 bytes.
func DecodeSalt(s string) ([]byte, error) {
	if len(s) < 2*MinSaltLength || len(s) > 2*MaxSaltLength {
		return nil, errors.New("want 32 to 128 hex characters")
	}
	return decodeLowerHex(s)
}

// decodeLowerHex accepts only lowercase hex, so a value has one text form.
func decodeLowerHex(s string) ([]byte, error) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return nil, errors.New("only lowercase hex is allowed")
		}
	}
	return hex.DecodeString(s)
}

// ValidResult reports whether v is a result a prober may reveal.
func ValidResult(v uint64) bool {
	return v == ResultPass || v == ResultFail || v == ResultTamper
}

// Majority is the number of matching reports that decides a node's outcome:
// more than half of the probers assigned to it.
func Majority(perNode uint64) uint64 {
	return perNode/2 + 1
}

// Tally decides one node's outcome for a round from the opened reports.
// A report of tampering counts as a fail vote. tamperProven is true only if
// every assigned prober reported tampering.
func Tally(pass, fail, tamper, perNode uint64) (outcome string, tamperProven bool) {
	need := Majority(perNode)
	switch {
	case pass >= need:
		outcome = OutcomePass
	case fail+tamper >= need:
		outcome = OutcomeFail
	default:
		outcome = OutcomeUnverified
	}
	tamperProven = perNode > 0 && tamper >= perNode
	return outcome, tamperProven
}
