package keeper_test

import (
	"encoding/binary"
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/verify/keeper"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

const (
	pass   = types.ResultPass
	fail   = types.ResultFail
	tamper = types.ResultTamper
	sealed = uint64(0)  // sealed but never opened
	absent = uint64(99) // no report at all
)

func TestTallyAllPass(t *testing.T) {
	w := newWorld(t)
	round := w.play(t, 1, [3]uint64{pass, pass, pass})

	rec := w.record(t, 1)
	require.True(t, rec.Verified)
	require.Equal(t, round, rec.LastRound)
	require.Equal(t, round, rec.LastPassRound)
	require.Equal(t, uint64(1), rec.Passes)
	require.Zero(t, rec.Fails)
	require.Zero(t, rec.ConsecutiveFails)
	require.Zero(t, rec.TamperRounds)

	for _, p := range w.p {
		got := w.prober(t, p.id)
		require.Equal(t, uint64(1), got.Accepted)
		require.Zero(t, got.Outliers)
	}
	// Counted reports are deleted.
	require.Zero(t, w.reportCount(t))
	require.True(t, w.hasEvent("verify_tally"))

	res, err := w.qs.NodeRecord(w.ctx, &types.QueryNodeRecordRequest{NodeId: 1})
	require.NoError(t, err)
	require.Equal(t, rec, res.NodeRecord)

	// The other node had no reports, so it has no record.
	has, err := w.keeper.NodeRecord.Has(w.ctx, 2)
	require.NoError(t, err)
	require.False(t, has)
}

// The tally waits until the window for opening reports has closed.
func TestTallyWaitsForRevealWindow(t *testing.T) {
	w := newWorld(t)
	for _, p := range w.p {
		w.commit(t, p, 1, 1, pass, 0)
	}
	w.nextRound(t) // round 2: reports are being opened, nothing is counted
	for _, p := range w.p {
		w.reveal(t, p, 1, 1, pass, 0)
	}
	w.nextBlocks(t, testRoundBlocks-1) // last block of round 2
	has, err := w.keeper.NodeRecord.Has(w.ctx, 1)
	require.NoError(t, err)
	require.False(t, has)
	require.Equal(t, 3, w.reportCount(t))

	w.nextBlocks(t, 1) // round 3
	require.True(t, w.record(t, 1).Verified)
}

func TestTallyOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		results  [3]uint64
		verified bool
		passes   uint64
		fails    uint64
		tampers  uint64
		accepted [3]uint64
		outliers [3]uint64
	}{
		{"two pass, one fail", [3]uint64{pass, pass, fail}, true, 1, 0, 0, [3]uint64{1, 1, 0}, [3]uint64{0, 0, 1}},
		{"two pass, one says tamper", [3]uint64{pass, tamper, pass}, true, 1, 0, 0, [3]uint64{1, 0, 1}, [3]uint64{0, 1, 0}},
		{"two fail, one pass", [3]uint64{fail, pass, fail}, false, 0, 1, 0, [3]uint64{1, 0, 1}, [3]uint64{0, 1, 0}},
		{"three fail", [3]uint64{fail, fail, fail}, false, 0, 1, 0, [3]uint64{1, 1, 1}, [3]uint64{0, 0, 0}},
		{"fail and tamper agree the node is bad", [3]uint64{fail, tamper, pass}, false, 0, 1, 0, [3]uint64{1, 1, 0}, [3]uint64{0, 0, 1}},
		{"two tamper is a fail, not proven tampering", [3]uint64{tamper, tamper, pass}, false, 0, 1, 0, [3]uint64{1, 1, 0}, [3]uint64{0, 0, 1}},
		{"three tamper is proven tampering", [3]uint64{tamper, tamper, tamper}, false, 0, 1, 1, [3]uint64{1, 1, 1}, [3]uint64{0, 0, 0}},
		{"two pass, one never opened", [3]uint64{pass, pass, sealed}, true, 1, 0, 0, [3]uint64{1, 1, 0}, [3]uint64{0, 0, 1}},
		{"two pass, one absent", [3]uint64{pass, absent, pass}, true, 1, 0, 0, [3]uint64{1, 0, 1}, [3]uint64{0, 0, 0}},
		// No majority: the node is not verified, nothing counts against it,
		// and opened reports are not scored.
		{"one pass, one fail, one absent", [3]uint64{pass, fail, absent}, false, 0, 0, 0, [3]uint64{0, 0, 0}, [3]uint64{0, 0, 0}},
		{"one pass only", [3]uint64{pass, absent, absent}, false, 0, 0, 0, [3]uint64{0, 0, 0}, [3]uint64{0, 0, 0}},
		// One prober alone can never fail a node.
		{"one fail only", [3]uint64{absent, fail, absent}, false, 0, 0, 0, [3]uint64{0, 0, 0}, [3]uint64{0, 0, 0}},
		{"one tamper only", [3]uint64{absent, absent, tamper}, false, 0, 0, 0, [3]uint64{0, 0, 0}, [3]uint64{0, 0, 0}},
		{"one fail, two never opened", [3]uint64{fail, sealed, sealed}, false, 0, 0, 0, [3]uint64{0, 0, 0}, [3]uint64{0, 1, 1}},
		{"nothing opened", [3]uint64{sealed, sealed, sealed}, false, 0, 0, 0, [3]uint64{0, 0, 0}, [3]uint64{1, 1, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			round := w.play(t, 1, tc.results)

			rec := w.record(t, 1)
			require.Equal(t, tc.verified, rec.Verified)
			require.Equal(t, tc.passes, rec.Passes)
			require.Equal(t, tc.fails, rec.Fails)
			require.Equal(t, tc.fails, rec.ConsecutiveFails)
			require.Equal(t, tc.tampers, rec.TamperRounds)
			require.Equal(t, round, rec.LastRound)

			for i, p := range w.p {
				got := w.prober(t, p.id)
				require.Equal(t, tc.accepted[i], got.Accepted, "accepted count of prober %d", i)
				require.Equal(t, tc.outliers[i], got.Outliers, "outlier count of prober %d", i)
			}
			require.Zero(t, w.reportCount(t))
			// This version never jails.
			require.False(t, w.hasEvent("verify_would_jail"))
		})
	}
}

// Failed rounds in a row are counted, a pass resets the count, and the
// would-jail event appears at the threshold. Nothing is penalised.
func TestConsecutiveFails(t *testing.T) {
	w := newWorld(t)
	w.setParams(t, func(p *types.Params) { p.FailsBeforeJail = 3 })

	w.play(t, 1, [3]uint64{fail, fail, fail})
	w.play(t, 1, [3]uint64{fail, fail, pass})
	rec := w.record(t, 1)
	require.Equal(t, uint64(2), rec.ConsecutiveFails)
	require.False(t, w.hasEvent("verify_would_jail"))

	// An undecided round neither adds to the count nor resets it.
	w.play(t, 1, [3]uint64{fail, absent, absent})
	require.Equal(t, uint64(2), w.record(t, 1).ConsecutiveFails)

	w.play(t, 1, [3]uint64{fail, fail, fail})
	rec = w.record(t, 1)
	require.Equal(t, uint64(3), rec.ConsecutiveFails)
	require.Equal(t, uint64(3), rec.Fails)
	require.True(t, w.hasEvent("verify_would_jail"))

	w.play(t, 1, [3]uint64{pass, pass, pass})
	rec = w.record(t, 1)
	require.True(t, rec.Verified)
	require.Zero(t, rec.ConsecutiveFails)
	require.Equal(t, uint64(3), rec.Fails) // the total is kept
	require.Equal(t, uint64(1), rec.Passes)

	// Bonds were never touched.
	w.requireBondsExact(t)
	requireInt(t, minBond*3, w.moduleBalance())
}

// Two nodes in the same round are tallied separately.
func TestTallyTwoNodes(t *testing.T) {
	w := newWorld(t)
	for _, p := range w.p {
		w.commit(t, p, 1, 1, pass, 1)
		w.commit(t, p, 1, 2, fail, 0)
	}
	w.nextRound(t)
	for _, p := range w.p {
		w.reveal(t, p, 1, 1, pass, 1)
		w.reveal(t, p, 1, 2, fail, 0)
	}
	w.nextRound(t)

	require.True(t, w.record(t, 1).Verified)
	require.False(t, w.record(t, 2).Verified)
	require.Equal(t, uint64(1), w.record(t, 2).Fails)
	for _, p := range w.p {
		require.Equal(t, uint64(2), w.prober(t, p.id).Accepted)
	}
}

// Reports for a node that has left the registry are deleted and leave no
// record behind.
func TestTallyNodeGone(t *testing.T) {
	w := newWorld(t)
	w.play(t, 1, [3]uint64{pass, pass, pass})
	require.True(t, w.record(t, 1).Verified)

	round := w.round(t)
	for _, p := range w.p {
		w.commit(t, p, round, 1, pass, 0)
	}
	delete(w.registry.nodes, 1)
	w.nextRound(t)
	for _, p := range w.p {
		w.reveal(t, p, round, 1, pass, 0)
	}
	w.nextRound(t)

	has, err := w.keeper.NodeRecord.Has(w.ctx, 1)
	require.NoError(t, err)
	require.False(t, has)
	require.Zero(t, w.reportCount(t))
}

// A prober that has withdrawn before the tally is simply skipped.
func TestTallySkipsMissingProber(t *testing.T) {
	w := newWorld(t)
	for _, p := range w.p {
		w.commit(t, p, 1, 1, pass, 0)
	}
	w.nextRound(t)
	for _, p := range w.p {
		w.reveal(t, p, 1, 1, pass, 0)
	}
	require.NoError(t, w.keeper.Prober.Remove(w.ctx, w.p[2].id))
	w.nextRound(t)
	require.True(t, w.record(t, 1).Verified)
	require.Equal(t, uint64(1), w.prober(t, w.p[0].id).Accepted)
}

// putReport writes a report straight into the store.
func putReport(t *testing.T, f *fixture, round, nodeID, proberID, result uint64) {
	t.Helper()
	suffix := make([]byte, 16)
	binary.BigEndian.PutUint64(suffix[:8], nodeID)
	binary.BigEndian.PutUint64(suffix[8:], proberID)
	value := append(make([]byte, types.HashLength), byte(result), 0)
	require.NoError(t, f.keeper.Reports.Set(f.ctx, collections.Join(round, suffix), value))
}

// The tally does a bounded amount of work per block, finishes the node it is
// on, and picks up where it left off in the next block.
func TestTallyIsBounded(t *testing.T) {
	f := initFixture(t)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)

	const nodes = 250 // three reports each: 750 reports
	for n := uint64(1); n <= nodes; n++ {
		f.addNode(n, registrytypes.StatusActive)
		for p := uint64(0); p < 3; p++ {
			putReport(t, f, 1, n, p, pass)
		}
	}
	require.Equal(t, nodes*3, f.reportCount(t))

	// Rounds 1 and 2 are not due while the current round is below 3.
	require.NoError(t, f.keeper.TallyDue(f.ctx, 2, params))
	require.Equal(t, nodes*3, f.reportCount(t))

	left := nodes * 3
	for call := 1; left > 0; call++ {
		require.LessOrEqual(t, call, 4, "tally did not finish in the expected number of blocks")
		require.NoError(t, f.keeper.TallyDue(f.ctx, 3, params))
		now := f.reportCount(t)
		done := left - now
		require.Positive(t, done)
		require.LessOrEqual(t, done, keeper.MaxTallyReportsPerBlock+3)
		require.Zero(t, now%3, "a node's reports were split across blocks")
		left = now
	}

	// Every node got exactly one tally.
	for n := uint64(1); n <= nodes; n++ {
		rec, err := f.keeper.NodeRecord.Get(f.ctx, n)
		require.NoError(t, err)
		require.Equal(t, uint64(1), rec.Passes, "node %d", n)
	}
}

// Older rounds are tallied before newer ones, and rounds whose window is
// still open are left alone.
func TestTallyOrderAndDueRounds(t *testing.T) {
	f := initFixture(t)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	f.addNode(1, registrytypes.StatusActive)
	for round := uint64(1); round <= 4; round++ {
		for p := uint64(0); p < 3; p++ {
			result := uint64(pass)
			if round == 2 {
				result = fail
			}
			putReport(t, f, round, 1, p, result)
		}
	}

	// In round 4, rounds 1 and 2 are due; 3 and 4 are not.
	require.NoError(t, f.keeper.TallyDue(f.ctx, 4, params))
	require.Equal(t, 6, f.reportCount(t))
	rec, err := f.keeper.NodeRecord.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(2), rec.LastRound)
	require.Equal(t, uint64(1), rec.LastPassRound)
	require.Equal(t, uint64(1), rec.Passes)
	require.Equal(t, uint64(1), rec.Fails)
	require.False(t, rec.Verified) // round 2, the later one, was a fail
}

// A malformed stored report cannot block the tally.
func TestTallySurvivesMalformedReport(t *testing.T) {
	f := initFixture(t)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	f.addNode(1, registrytypes.StatusActive)
	require.NoError(t, f.keeper.Reports.Set(f.ctx, collections.Join(uint64(1), []byte{1, 2, 3}), []byte{9}))
	for p := uint64(0); p < 3; p++ {
		putReport(t, f, 1, 1, p, pass)
	}
	require.NoError(t, f.keeper.TallyDue(f.ctx, 3, params))
	require.Zero(t, f.reportCount(t))
	rec, err := f.keeper.NodeRecord.Get(f.ctx, 1)
	require.NoError(t, err)
	require.True(t, rec.Verified)
}
