// Package probe holds the prober program's rules: what a probe measures,
// how a measurement becomes a report, and how reports are sealed and kept
// until they are opened.
//
// A prober is an ordinary paying customer. It connects through two hops,
// as customers do, and so it judges one node at a time against a partner
// node of the other tier: an entry node through some exit, an exit node
// through some entry. Problems that belong to the partner are never blamed
// on the node being judged; such a probe sends no report at all.
package probe

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
)

// Report results, as the verification module numbers them.
const (
	ResultPass   uint64 = 1
	ResultFail   uint64 = 2
	ResultTamper uint64 = 3
)

// Role is the tier of the node being judged.
type Role int

const (
	Entry Role = 1
	Exit  Role = 2
)

// Hop says how far a connection got.
type Hop int

const (
	// NoHop: the entry node could not be reached or refused payment.
	NoHop Hop = iota
	// EntryOnly: the entry tunnel came up, the exit did not.
	EntryOnly
	// BothHops: both tunnels came up.
	BothHops
)

// Measurement is what one probe saw.
type Measurement struct {
	Reached Hop
	// Mbps is the measured download speed through both hops; 0 if none.
	Mbps float64
	// IntegrityOK is false if known content came back altered.
	IntegrityOK bool
	// DNSOK is false if a name lookup was answered falsely.
	DNSOK bool
	// Checked is false if the measurements could not be made at all.
	Checked bool
}

// Verdict turns a measurement into a report. ok is false when the probe
// says nothing reliable about the node being judged, and no report should
// be sent.
func Verdict(role Role, m Measurement, minMbps float64) (result, speedBand uint64, ok bool) {
	switch role {
	case Entry:
		switch m.Reached {
		case NoHop:
			return ResultFail, 0, true
		case EntryOnly:
			// The partner exit failed; nothing is known about the entry's
			// service.
			return 0, 0, false
		}
	case Exit:
		switch m.Reached {
		case NoHop:
			// The partner entry failed.
			return 0, 0, false
		case EntryOnly:
			return ResultFail, 0, true
		}
	default:
		return 0, 0, false
	}
	if !m.Checked {
		// Both tunnels came up but nothing could be fetched through them.
		if role == Exit {
			return ResultFail, 0, true
		}
		return 0, 0, false
	}
	band := SpeedBand(m.Mbps)
	if !m.IntegrityOK || !m.DNSOK {
		// Only an exit can see and alter the traffic; an entry carries the
		// inner tunnel without being able to read it.
		if role == Exit {
			return ResultTamper, band, true
		}
		return 0, 0, false
	}
	if m.Mbps < minMbps {
		return ResultFail, band, true
	}
	return ResultPass, band, true
}

// SpeedBand is the measured speed in whole megabits per second, capped at
// 255, the most a report can carry.
func SpeedBand(mbps float64) uint64 {
	if mbps <= 0 || math.IsNaN(mbps) {
		return 0
	}
	if mbps >= 255 {
		return 255
	}
	return uint64(math.Round(mbps))
}

const domainCommit = "SMARTNODES-VERIFY-COMMIT-V1"

// Seal is the sealed form of a report, exactly as the verification module
// computes it, in lowercase hex.
func Seal(chainID string, round, nodeID, proberID, result, speedBand uint64, salt []byte) string {
	be := func(v uint64) []byte {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], v)
		return b[:]
	}
	h := sha256.New()
	h.Write([]byte(domainCommit))
	h.Write(be(uint64(len(chainID))))
	h.Write([]byte(chainID))
	h.Write(be(round))
	h.Write(be(nodeID))
	h.Write(be(proberID))
	h.Write(be(result))
	h.Write(be(speedBand))
	h.Write(be(uint64(len(salt))))
	h.Write(salt)
	return hex.EncodeToString(h.Sum(nil))
}
