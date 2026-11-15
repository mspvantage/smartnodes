package keeper_test

import (
	"encoding/hex"
	"strings"
	"testing"

	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/stretchr/testify/require"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/verify/keeper"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

func TestRoundsAdvance(t *testing.T) {
	f := initFixture(t)

	// Before the first block there is no round.
	require.Equal(t, uint64(0), f.round(t))
	res, err := f.qs.Round(f.ctx, &types.QueryRoundRequest{})
	require.NoError(t, err)
	require.Equal(t, uint64(0), res.Round)

	f.begin(t)
	require.Equal(t, uint64(1), f.round(t))
	require.True(t, f.hasEvent("verify_new_round"))

	seeds := map[string]bool{}
	for want := uint64(1); want <= 6; want++ {
		round, start, err := f.keeper.CurrentRound(f.ctx)
		require.NoError(t, err)
		require.Equal(t, want, round)
		require.Equal(t, int64(startHeight)+int64(want-1)*testRoundBlocks, start)

		seed, err := f.keeper.RoundSeed.Get(f.ctx, round)
		require.NoError(t, err)
		require.Len(t, seed, types.HashLength)
		require.False(t, seeds[string(seed)], "round %d reused a seed", round)
		seeds[string(seed)] = true

		res, err := f.qs.Round(f.ctx, &types.QueryRoundRequest{})
		require.NoError(t, err)
		require.Equal(t, round, res.Round)
		require.Equal(t, start, res.StartHeight)
		require.Equal(t, start+testRoundBlocks, res.NextRoundHeight)
		require.Equal(t, hex.EncodeToString(seed), res.Seed)

		// The round does not change until its last block has passed.
		f.nextBlocks(t, testRoundBlocks-1)
		require.Equal(t, want, f.round(t))
		f.nextBlocks(t, 1)
	}

	// Only the most recent seeds are kept.
	current := f.round(t)
	for round := uint64(1); round <= current; round++ {
		has, err := f.keeper.RoundSeed.Has(f.ctx, round)
		require.NoError(t, err)
		require.Equal(t, round+keeper.SeedsKept > current, has, "seed of round %d", round)
	}
}

// Changing the round length must never make the round number jump or go
// backwards.
func TestRoundLengthChange(t *testing.T) {
	f := initFixture(t)
	f.begin(t)
	f.nextRound(t)
	f.nextRound(t)
	require.Equal(t, uint64(3), f.round(t))

	// A much longer round: the current round simply lasts longer.
	f.setParams(t, func(p *types.Params) { p.RoundBlocks = 50 })
	f.nextBlocks(t, 49)
	require.Equal(t, uint64(3), f.round(t))
	f.nextBlocks(t, 1)
	require.Equal(t, uint64(4), f.round(t))

	// A much shorter round: the next block starts one new round, not many.
	f.nextBlocks(t, 20)
	f.setParams(t, func(p *types.Params) { p.RoundBlocks = 5 })
	f.nextBlocks(t, 1)
	require.Equal(t, uint64(5), f.round(t))
	f.nextBlocks(t, 5)
	require.Equal(t, uint64(6), f.round(t))
}

// A fault in the start-of-block work must never stop the chain.
func TestBeginBlockerNeverFails(t *testing.T) {
	f := initFixture(t)
	f.begin(t)
	require.NoError(t, f.keeper.Params.Remove(f.ctx)) // breaks the work
	f.nextRound(t)                                    // must not fail
	require.True(t, f.hasEvent("verify_begin_block_failed"))
	// Nothing half-done was kept: the round did not move.
	require.Equal(t, uint64(1), f.round(t))
}

func TestCommitReports(t *testing.T) {
	w := newWorld(t)
	p := w.p[0]

	// One message can carry reports for several nodes.
	_, err := w.ms.CommitReports(w.ctx, &types.MsgCommitReports{
		Owner:   p.str,
		Round:   1,
		NodeIds: []uint64{1, 2},
		Hashes:  []string{seal(1, 1, p.id, types.ResultPass, 3), seal(1, 2, p.id, types.ResultFail, 0)},
	})
	require.NoError(t, err)
	require.Equal(t, 2, w.reportCount(t))
	require.True(t, w.hasEvent("verify_commit"))
}

func TestCommitReportsRejected(t *testing.T) {
	w := newWorld(t)
	p := w.p[0]
	w.addNode(3, registrytypes.StatusUnbonding)
	good := seal(1, 1, p.id, types.ResultPass, 0)

	msg := func(change func(*types.MsgCommitReports)) *types.MsgCommitReports {
		m := &types.MsgCommitReports{Owner: p.str, Round: 1, NodeIds: []uint64{1}, Hashes: []string{good}}
		change(m)
		return m
	}
	_, stranger := w.account(t, 0x77)

	cases := []struct {
		name string
		msg  *types.MsgCommitReports
		want error
	}{
		{"no reports", msg(func(m *types.MsgCommitReports) { m.NodeIds, m.Hashes = nil, nil }), types.ErrInvalidReport},
		{"more hashes than nodes", msg(func(m *types.MsgCommitReports) { m.Hashes = []string{good, good} }), types.ErrInvalidReport},
		{"more nodes than hashes", msg(func(m *types.MsgCommitReports) { m.NodeIds = []uint64{1, 2} }), types.ErrInvalidReport},
		{"same node twice", msg(func(m *types.MsgCommitReports) { m.NodeIds, m.Hashes = []uint64{1, 1}, []string{good, good} }), types.ErrInvalidReport},
		{"past round", msg(func(m *types.MsgCommitReports) { m.Round = 0 }), types.ErrWrongRound},
		{"future round", msg(func(m *types.MsgCommitReports) { m.Round = 2 }), types.ErrWrongRound},
		{"not a prober", msg(func(m *types.MsgCommitReports) { m.Owner = stranger }), types.ErrProberNotFound},
		{"hash too short", msg(func(m *types.MsgCommitReports) { m.Hashes = []string{good[:62]} }), types.ErrInvalidReport},
		{"hash in uppercase", msg(func(m *types.MsgCommitReports) { m.Hashes = []string{strings.ToUpper(good)} }), types.ErrInvalidReport},
		{"hash not hex", msg(func(m *types.MsgCommitReports) { m.Hashes = []string{"zz" + good[2:]} }), types.ErrInvalidReport},
		{"unknown node", msg(func(m *types.MsgCommitReports) { m.NodeIds = []uint64{99} }), registrytypes.ErrNodeNotFound},
		{"node that is unbonding", msg(func(m *types.MsgCommitReports) { m.NodeIds = []uint64{3} }), types.ErrNodeNotActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := w.ms.CommitReports(w.scratch(), tc.msg)
			require.ErrorIs(t, err, tc.want)
		})
	}

	t.Run("invalid address", func(t *testing.T) {
		_, err := w.ms.CommitReports(w.scratch(), msg(func(m *types.MsgCommitReports) { m.Owner = "nope" }))
		require.Error(t, err)
	})

	t.Run("too many reports in one message", func(t *testing.T) {
		m := &types.MsgCommitReports{Owner: p.str, Round: 1}
		for i := 0; i <= types.MaxCommitsPerMsg; i++ {
			m.NodeIds = append(m.NodeIds, uint64(1000+i))
			m.Hashes = append(m.Hashes, good)
		}
		_, err := w.ms.CommitReports(w.scratch(), m)
		require.ErrorIs(t, err, types.ErrInvalidReport)
	})

	t.Run("a second sealed report for the same node", func(t *testing.T) {
		w.commit(t, p, 1, 1, types.ResultPass, 0)
		_, err := w.ms.CommitReports(w.scratch(), msg(func(m *types.MsgCommitReports) {
			m.Hashes = []string{seal(1, 1, p.id, types.ResultFail, 0)}
		}))
		require.ErrorIs(t, err, types.ErrAlreadyCommitted)
	})

	t.Run("a prober that has not joined yet", func(t *testing.T) {
		late := w.newProber(t, 0x55)
		_, err := w.ms.CommitReports(w.scratch(), &types.MsgCommitReports{
			Owner: late.str, Round: 1, NodeIds: []uint64{1}, Hashes: []string{seal(1, 1, late.id, types.ResultPass, 0)},
		})
		require.ErrorIs(t, err, types.ErrProberNotActive)
	})
}

func TestCommitBeforeFirstRound(t *testing.T) {
	f := initFixture(t)
	p := f.newProber(t, 1)
	_, err := f.ms.CommitReports(f.ctx, &types.MsgCommitReports{
		Owner: p.str, Round: 0, NodeIds: []uint64{1}, Hashes: []string{seal(0, 1, p.id, types.ResultPass, 0)},
	})
	require.ErrorIs(t, err, types.ErrNoRound)
	_, err = f.ms.RevealReport(f.ctx, revealMsg(p, 0, 1, types.ResultPass, 0))
	require.ErrorIs(t, err, types.ErrNoRound)
}

// With more probers than slots, only the assigned probers may report on a
// node, and the Assignment query names exactly those probers.
func TestOnlyAssignedProbersMayCommit(t *testing.T) {
	f := initFixture(t)
	var ps []*tester
	for i := 0; i < 7; i++ {
		ps = append(ps, f.newProber(t, byte(0x30+i)))
	}
	f.addNode(1, registrytypes.StatusActive)
	f.begin(t)

	res, err := f.qs.Assignment(f.ctx, &types.QueryAssignmentRequest{NodeId: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.Round)
	require.True(t, res.Selected)
	require.Len(t, res.ProberIds, 3)
	assigned := map[uint64]bool{}
	for _, id := range res.ProberIds {
		require.False(t, assigned[id], "prober %d assigned twice", id)
		assigned[id] = true
	}

	accepted := 0
	for _, p := range ps {
		_, err := f.ms.CommitReports(f.scratch(), &types.MsgCommitReports{
			Owner: p.str, Round: 1, NodeIds: []uint64{1}, Hashes: []string{seal(1, 1, p.id, types.ResultPass, 0)},
		})
		if assigned[p.id] {
			require.NoError(t, err, "assigned prober %d was refused", p.id)
			accepted++
		} else {
			require.ErrorIs(t, err, types.ErrNotAssigned, "unassigned prober %d", p.id)
		}
	}
	require.Equal(t, 3, accepted)

	// A new round draws again; the assignment is still three distinct
	// probers from the set.
	f.nextRound(t)
	res2, err := f.qs.Assignment(f.ctx, &types.QueryAssignmentRequest{NodeId: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(2), res2.Round)
	require.Len(t, res2.ProberIds, 3)
}

// When only a share of nodes is probed each round, reports on the others
// are refused.
func TestOnlySelectedNodesAreProbed(t *testing.T) {
	f := initFixture(t)
	p := f.newProber(t, 1)
	f.setParams(t, func(p *types.Params) { p.SampleBps = 5000 })
	for id := uint64(1); id <= 40; id++ {
		f.addNode(id, registrytypes.StatusActive)
	}
	f.begin(t)

	selected, skipped := 0, 0
	for id := uint64(1); id <= 40; id++ {
		res, err := f.qs.Assignment(f.ctx, &types.QueryAssignmentRequest{NodeId: id})
		require.NoError(t, err)
		_, err = f.ms.CommitReports(f.scratch(), &types.MsgCommitReports{
			Owner: p.str, Round: 1, NodeIds: []uint64{id}, Hashes: []string{seal(1, id, p.id, types.ResultPass, 0)},
		})
		if res.Selected {
			require.NoError(t, err, "selected node %d", id)
			require.Equal(t, []uint64{p.id}, res.ProberIds)
			selected++
		} else {
			require.ErrorIs(t, err, types.ErrNodeNotSelected, "node %d", id)
			require.Empty(t, res.ProberIds)
			skipped++
		}
	}
	// Half the nodes on average; with 40 nodes both groups are present.
	require.Positive(t, selected)
	require.Positive(t, skipped)
}

func TestRevealReport(t *testing.T) {
	w := newWorld(t)
	p := w.p[0]
	w.commit(t, p, 1, 1, types.ResultPass, 7)

	t.Run("cannot open in the round it was sealed", func(t *testing.T) {
		_, err := w.ms.RevealReport(w.scratch(), revealMsg(p, 1, 1, types.ResultPass, 7))
		require.ErrorIs(t, err, types.ErrWrongRound)
	})

	w.nextRound(t) // round 2: round 1 reports can be opened

	bad := func(change func(*types.MsgRevealReport)) *types.MsgRevealReport {
		m := revealMsg(p, 1, 1, types.ResultPass, 7)
		change(m)
		return m
	}
	cases := []struct {
		name string
		msg  *types.MsgRevealReport
		want error
	}{
		{"result 0", bad(func(m *types.MsgRevealReport) { m.Result = 0 }), types.ErrInvalidReport},
		{"result 4", bad(func(m *types.MsgRevealReport) { m.Result = 4 }), types.ErrInvalidReport},
		{"speed band too high", bad(func(m *types.MsgRevealReport) { m.SpeedBand = 256 }), types.ErrInvalidReport},
		{"salt too short", bad(func(m *types.MsgRevealReport) { m.Salt = "abcd" }), types.ErrInvalidReport},
		{"salt in uppercase", bad(func(m *types.MsgRevealReport) { m.Salt = strings.ToUpper(m.Salt) + "AA" }), types.ErrInvalidReport},
		{"proof too long", bad(func(m *types.MsgRevealReport) { m.Proof = strings.Repeat("x", types.MaxProofLength+1) }), types.ErrInvalidReport},
		{"wrong round", bad(func(m *types.MsgRevealReport) { m.Round = 2 }), types.ErrWrongRound},
		{"node with no sealed report", bad(func(m *types.MsgRevealReport) { m.NodeId = 2 }), types.ErrNoCommit},
		{"a different result than sealed", bad(func(m *types.MsgRevealReport) { m.Result = types.ResultFail }), types.ErrRevealMismatch},
		{"a different speed than sealed", bad(func(m *types.MsgRevealReport) { m.SpeedBand = 8 }), types.ErrRevealMismatch},
		{"a different salt than sealed", bad(func(m *types.MsgRevealReport) { m.Salt = hex.EncodeToString(salt(9, 9)) }), types.ErrRevealMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := w.ms.RevealReport(w.scratch(), tc.msg)
			require.ErrorIs(t, err, tc.want)
		})
	}

	t.Run("another prober cannot open it", func(t *testing.T) {
		other := w.p[1]
		m := revealMsg(p, 1, 1, types.ResultPass, 7)
		m.Owner = other.str
		_, err := w.ms.RevealReport(w.scratch(), m)
		require.ErrorIs(t, err, types.ErrNoCommit)
	})
	t.Run("not a prober", func(t *testing.T) {
		_, stranger := w.account(t, 0x77)
		m := revealMsg(p, 1, 1, types.ResultPass, 7)
		m.Owner = stranger
		_, err := w.ms.RevealReport(w.scratch(), m)
		require.ErrorIs(t, err, types.ErrProberNotFound)
	})

	t.Run("the sealed values open it", func(t *testing.T) {
		m := revealMsg(p, 1, 1, types.ResultPass, 7)
		m.Proof = "ignored-in-this-version"
		_, err := w.ms.RevealReport(w.ctx, m)
		require.NoError(t, err)
		require.True(t, w.hasEvent("verify_reveal"))
	})
	t.Run("cannot open twice", func(t *testing.T) {
		_, err := w.ms.RevealReport(w.scratch(), revealMsg(p, 1, 1, types.ResultPass, 7))
		require.ErrorIs(t, err, types.ErrAlreadyRevealed)
	})
}

// A report left sealed past its window can never be opened.
func TestRevealWindowCloses(t *testing.T) {
	w := newWorld(t)
	w.commit(t, w.p[0], 1, 1, types.ResultPass, 0)
	w.nextRound(t)
	w.nextRound(t) // round 3: too late for round 1
	_, err := w.ms.RevealReport(w.scratch(), revealMsg(w.p[0], 1, 1, types.ResultPass, 0))
	require.ErrorIs(t, err, types.ErrWrongRound)
}

// A sealed report from one chain or round cannot be replayed in another:
// the same values hash differently.
func TestSealIsBoundToRoundAndProber(t *testing.T) {
	w := newWorld(t)
	p, q := w.p[0], w.p[1]

	// q posts p's sealed hash as its own. It cannot open it, because the
	// hash covers the prober id.
	stolen := seal(1, 1, p.id, types.ResultPass, 0)
	_, err := w.ms.CommitReports(w.ctx, &types.MsgCommitReports{Owner: q.str, Round: 1, NodeIds: []uint64{1}, Hashes: []string{stolen}})
	require.NoError(t, err)
	w.nextRound(t)
	m := revealMsg(p, 1, 1, types.ResultPass, 0)
	m.Owner = q.str
	_, err = w.ms.RevealReport(w.scratch(), m)
	require.ErrorIs(t, err, types.ErrRevealMismatch)

	// A hash sealed for round 1, posted again in round 2, cannot be opened
	// as a round 2 report.
	_, err = w.ms.CommitReports(w.ctx, &types.MsgCommitReports{Owner: p.str, Round: 2, NodeIds: []uint64{1}, Hashes: []string{stolen}})
	require.NoError(t, err)
	w.nextRound(t)
	_, err = w.ms.RevealReport(w.scratch(), revealMsg(p, 2, 1, types.ResultPass, 0))
	require.ErrorIs(t, err, types.ErrRevealMismatch)
}

func TestQueryNodeRecordNotFound(t *testing.T) {
	f := initFixture(t)
	_, err := f.qs.NodeRecord(f.ctx, &types.QueryNodeRecordRequest{NodeId: 5})
	require.ErrorIs(t, err, sdkerrors.ErrKeyNotFound)
	_, err = f.qs.NodeRecord(f.ctx, nil)
	require.Error(t, err)
	_, err = f.qs.Round(f.ctx, nil)
	require.Error(t, err)
	_, err = f.qs.Assignment(f.ctx, nil)
	require.Error(t, err)

	// Before the first round nothing is assigned.
	res, err := f.qs.Assignment(f.ctx, &types.QueryAssignmentRequest{NodeId: 1})
	require.NoError(t, err)
	require.False(t, res.Selected)
}
