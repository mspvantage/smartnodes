package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

var (
	allPass   = [3]uint64{pass, pass, pass}
	allFail   = [3]uint64{fail, fail, fail}
	allTamper = [3]uint64{tamper, tamper, tamper}
)

// newLiveWorld is a world with penalties switched on and a low jail
// threshold.
func newLiveWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.setParams(t, func(p *types.Params) {
		p.ReportOnly = false
		p.FailsBeforeJail = 2
	})
	return w
}

func (w *world) unjail(t *testing.T, nodeID uint64) error {
	t.Helper()
	_, err := w.ms.Unjail(w.scratch(), &types.MsgUnjail{Operator: w.operator(), NodeId: nodeID})
	return err
}

// A node that fails enough rounds in a row is jailed. Its bond is not
// touched: being offline costs listing, never bond.
func TestJailAfterConsecutiveFails(t *testing.T) {
	w := newLiveWorld(t)

	w.play(t, 1, allFail)
	rec := w.record(t, 1)
	require.False(t, rec.Jailed)
	require.Equal(t, uint64(1), rec.ConsecutiveFails)

	w.play(t, 1, allFail)
	rec = w.record(t, 1)
	require.True(t, rec.Jailed)
	require.False(t, rec.Verified)
	require.Equal(t, uint64(1), rec.JailCount)
	require.Equal(t, w.sdkCtx.BlockTime().Unix(), rec.JailedAt)
	require.Zero(t, rec.Slashed)
	require.True(t, w.hasEvent("verify_jail"))
	require.False(t, w.hasEvent("verify_slash"))
	requireInt(t, nodeBond, w.nodeBondLeft(1))
	require.True(t, w.registry.burned.IsZero())

	// A jailed node is not probed.
	res, err := w.qs.Assignment(w.ctx, &types.QueryAssignmentRequest{NodeId: 1})
	require.NoError(t, err)
	require.False(t, res.Selected)
	round := w.round(t)
	_, err = w.ms.CommitReports(w.scratch(), &types.MsgCommitReports{
		Owner: w.p[0].str, Round: round, NodeIds: []uint64{1}, Hashes: []string{seal(round, 1, w.p[0].id, pass, 0)},
	})
	require.ErrorIs(t, err, types.ErrNodeJailed)

	// The other node is unaffected.
	res, err = w.qs.Assignment(w.ctx, &types.QueryAssignmentRequest{NodeId: 2})
	require.NoError(t, err)
	require.True(t, res.Selected)
}

// In report-only mode the same failures are recorded but nothing is jailed.
func TestReportOnlyNeverJails(t *testing.T) {
	w := newWorld(t)
	w.setParams(t, func(p *types.Params) { p.FailsBeforeJail = 2 })
	w.play(t, 1, allFail)
	w.play(t, 1, allFail)
	w.play(t, 1, allFail)

	rec := w.record(t, 1)
	require.False(t, rec.Jailed)
	require.Zero(t, rec.JailCount)
	require.Equal(t, uint64(3), rec.ConsecutiveFails)
	require.True(t, w.hasEvent("verify_would_jail"))
	require.False(t, w.hasEvent("verify_jail"))
	require.ErrorIs(t, w.unjail(t, 1), types.ErrNodeNotJailed)
}

// A pass before the threshold resets the count, so the node is not jailed.
func TestPassResetsFailCount(t *testing.T) {
	w := newLiveWorld(t)
	w.play(t, 1, allFail)
	w.play(t, 1, allPass)
	w.play(t, 1, allFail)
	rec := w.record(t, 1)
	require.False(t, rec.Jailed)
	require.Equal(t, uint64(1), rec.ConsecutiveFails)
}

func TestUnjail(t *testing.T) {
	w := newLiveWorld(t)
	w.play(t, 1, allFail)
	w.play(t, 1, allFail)
	require.True(t, w.record(t, 1).Jailed)
	jailedAt := w.record(t, 1).JailedAt

	t.Run("not the operator", func(t *testing.T) {
		_, err := w.ms.Unjail(w.scratch(), &types.MsgUnjail{Operator: w.p[0].str, NodeId: 1})
		require.ErrorIs(t, err, types.ErrNotOperator)
	})
	t.Run("invalid address", func(t *testing.T) {
		_, err := w.ms.Unjail(w.scratch(), &types.MsgUnjail{Operator: "nope", NodeId: 1})
		require.Error(t, err)
	})
	t.Run("unknown node", func(t *testing.T) {
		require.ErrorIs(t, w.unjail(t, 99), registrytypes.ErrNodeNotFound)
	})
	t.Run("node that is not jailed", func(t *testing.T) {
		require.ErrorIs(t, w.unjail(t, 2), types.ErrNodeNotJailed)
	})
	t.Run("before the wait is over", func(t *testing.T) {
		// Two rounds of five-second blocks have not used up ten minutes.
		require.Less(t, w.sdkCtx.BlockTime().Unix(), jailedAt+int64(types.DefaultUnjailWaitSeconds))
		require.ErrorIs(t, w.unjail(t, 1), types.ErrJailNotOver)
	})
	t.Run("one second early", func(t *testing.T) {
		left := jailedAt + int64(types.DefaultUnjailWaitSeconds) - w.sdkCtx.BlockTime().Unix()
		w.advanceTime(time.Duration(left-1) * time.Second)
		require.ErrorIs(t, w.unjail(t, 1), types.ErrJailNotOver)
	})
	t.Run("a node that has started unbonding", func(t *testing.T) {
		w.advanceTime(time.Second)
		node := w.registry.nodes[1]
		node.Status = registrytypes.StatusUnbonding
		w.registry.nodes[1] = node
		require.ErrorIs(t, w.unjail(t, 1), types.ErrNodeNotActive)
		node.Status = registrytypes.StatusActive
		w.registry.nodes[1] = node
	})
	t.Run("the operator, after the wait", func(t *testing.T) {
		_, err := w.ms.Unjail(w.ctx, &types.MsgUnjail{Operator: w.operator(), NodeId: 1})
		require.NoError(t, err)
		rec := w.record(t, 1)
		require.False(t, rec.Jailed)
		require.Zero(t, rec.ConsecutiveFails)
		require.False(t, rec.Verified, "an unjailed node is not verified until it passes again")
		require.Equal(t, uint64(1), rec.JailCount)
		require.Equal(t, uint64(2), rec.Fails) // history is kept
		require.True(t, w.hasEvent("verify_unjail"))
	})
	t.Run("cannot unjail twice", func(t *testing.T) {
		require.ErrorIs(t, w.unjail(t, 1), types.ErrNodeNotJailed)
	})
	t.Run("it is probed again and can pass", func(t *testing.T) {
		w.play(t, 1, allPass)
		rec := w.record(t, 1)
		require.True(t, rec.Verified)
		require.False(t, rec.Jailed)
	})
	t.Run("it can be jailed a second time", func(t *testing.T) {
		w.play(t, 1, allFail)
		require.False(t, w.record(t, 1).Jailed) // one fail: the count restarted
		w.play(t, 1, allFail)
		rec := w.record(t, 1)
		require.True(t, rec.Jailed)
		require.Equal(t, uint64(2), rec.JailCount)
	})
}

// Reports still in flight when a node is jailed must not restart its wait.
func TestJailTimeIsNotRestarted(t *testing.T) {
	w := newLiveWorld(t)
	w.play(t, 1, allFail)

	// Seal failing reports in two consecutive rounds, so the second set is
	// already in flight when the first one jails the node.
	r1 := w.round(t)
	for _, p := range w.p {
		w.commit(t, p, r1, 1, fail, 0)
	}
	w.nextRound(t)
	r2 := w.round(t)
	for _, p := range w.p {
		w.reveal(t, p, r1, 1, fail, 0)
		w.commit(t, p, r2, 1, fail, 0)
	}
	w.nextRound(t) // r1 tallied: jailed
	rec := w.record(t, 1)
	require.True(t, rec.Jailed)
	jailedAt := rec.JailedAt
	for _, p := range w.p {
		w.reveal(t, p, r2, 1, fail, 0)
	}
	w.nextRound(t) // r2 tallied while jailed

	rec = w.record(t, 1)
	require.True(t, rec.Jailed)
	require.Equal(t, jailedAt, rec.JailedAt)
	require.Equal(t, uint64(1), rec.JailCount)
}

// Proven tampering burns part of the bond and jails the node at once.
func TestTamperSlash(t *testing.T) {
	w := newLiveWorld(t)
	w.setParams(t, func(p *types.Params) { p.FailsBeforeJail = 10 }) // jail must come from tampering alone
	w.play(t, 1, allTamper)

	want := nodeBond * types.DefaultTamperSlashBps / types.BpsDenominator
	rec := w.record(t, 1)
	require.True(t, rec.Jailed)
	require.Equal(t, uint64(1), rec.TamperRounds)
	require.Equal(t, want, rec.Slashed)
	requireInt(t, nodeBond-want, w.nodeBondLeft(1))
	requireInt(t, want, w.registry.burned)
	require.True(t, w.hasEvent("verify_slash"))
	require.True(t, w.hasEvent("verify_jail"))

	// The other node's bond is untouched, and prober bonds too.
	requireInt(t, nodeBond, w.nodeBondLeft(2))
	w.requireBondsExact(t)
}

// Tampering reported by fewer than all assigned probers fails the node but
// burns nothing. One or two probers can never cause a slash.
func TestNoSlashWithoutAllProbers(t *testing.T) {
	for name, results := range map[string][3]uint64{
		"two tamper, one pass":   {tamper, tamper, pass},
		"two tamper, one fail":   {tamper, fail, tamper},
		"two tamper, one silent": {tamper, tamper, sealed},
		"two tamper, one absent": {tamper, absent, tamper},
		"one tamper":             {absent, tamper, absent},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLiveWorld(t)
			w.play(t, 1, results)
			rec := w.record(t, 1)
			require.Zero(t, rec.Slashed)
			require.Zero(t, rec.TamperRounds)
			require.False(t, rec.Jailed)
			requireInt(t, nodeBond, w.nodeBondLeft(1))
			require.False(t, w.hasEvent("verify_slash"))
		})
	}
}

// In report-only mode proven tampering is recorded but nothing is burned.
func TestReportOnlyNeverSlashes(t *testing.T) {
	w := newWorld(t)
	w.play(t, 1, allTamper)
	rec := w.record(t, 1)
	require.Equal(t, uint64(1), rec.TamperRounds)
	require.Zero(t, rec.Slashed)
	require.False(t, rec.Jailed)
	requireInt(t, nodeBond, w.nodeBondLeft(1))
	require.True(t, w.hasEvent("verify_would_slash"))
	require.False(t, w.hasEvent("verify_slash"))
}

// With the slash set to zero, proven tampering still jails.
func TestTamperWithZeroSlashStillJails(t *testing.T) {
	w := newLiveWorld(t)
	w.setParams(t, func(p *types.Params) { p.TamperSlashBps = 0; p.FailsBeforeJail = 10 })
	w.play(t, 1, allTamper)
	rec := w.record(t, 1)
	require.True(t, rec.Jailed)
	require.Zero(t, rec.Slashed)
	requireInt(t, nodeBond, w.nodeBondLeft(1))
}

// A slash can never take more than the bond.
func TestSlashIsCappedAtTheBond(t *testing.T) {
	w := newLiveWorld(t)
	w.setParams(t, func(p *types.Params) { p.TamperSlashBps = types.BpsDenominator; p.UnjailWaitSeconds = 0 })
	w.play(t, 1, allTamper)
	requireInt(t, 0, w.nodeBondLeft(1))
	require.Equal(t, nodeBond, w.record(t, 1).Slashed)

	// Unjailed and caught again: there is nothing left to burn, and the
	// tally still completes.
	_, err := w.ms.Unjail(w.ctx, &types.MsgUnjail{Operator: w.operator(), NodeId: 1})
	require.NoError(t, err)
	w.play(t, 1, allTamper)
	rec := w.record(t, 1)
	require.True(t, rec.Jailed)
	require.Equal(t, uint64(2), rec.TamperRounds)
	require.Equal(t, nodeBond, rec.Slashed)
	requireInt(t, nodeBond, w.registry.burned)
}

// If one node's tally fails, its reports are dropped and recorded in an
// event, and every other node is still tallied.
func TestOneFailedTallyDoesNotStopOthers(t *testing.T) {
	w := newLiveWorld(t)
	w.registry.failSlash = true

	for _, p := range w.p {
		w.commit(t, p, 1, 1, tamper, 0) // node 1: the slash will fail
		w.commit(t, p, 1, 2, pass, 0)   // node 2: an ordinary pass
	}
	w.nextRound(t)
	for _, p := range w.p {
		w.reveal(t, p, 1, 1, tamper, 0)
		w.reveal(t, p, 1, 2, pass, 0)
	}
	w.nextRound(t)

	require.True(t, w.hasEvent("verify_tally_failed"))
	require.False(t, w.hasEvent("verify_begin_block_failed"))
	require.Zero(t, w.reportCount(t), "the failed node's reports must not be retried forever")

	// Node 1's half-finished tally left nothing behind.
	has, err := w.keeper.NodeRecord.Has(w.ctx, 1)
	require.NoError(t, err)
	require.False(t, has)
	// Node 2 was tallied normally, and its probers were scored once.
	require.True(t, w.record(t, 2).Verified)
	for _, p := range w.p {
		require.Equal(t, uint64(1), w.prober(t, p.id).Accepted)
	}
}

// Probers are paid from the prober fund for each accepted report.
func TestProberRewards(t *testing.T) {
	reward := types.DefaultReportReward

	t.Run("accepted reports are paid, outliers are not", func(t *testing.T) {
		w := newWorld(t)
		w.tickets.fund = math.NewIntFromUint64(reward * 10)
		w.play(t, 1, [3]uint64{pass, pass, fail})

		requireInt(t, reward, w.balance(w.p[0].addr))
		requireInt(t, reward, w.balance(w.p[1].addr))
		requireInt(t, 0, w.balance(w.p[2].addr))
		requireInt(t, reward*8, w.tickets.fund)
		require.True(t, w.hasEvent("verify_reward"))
		// Rewards never come out of bonds.
		w.requireBondsExact(t)
	})
	t.Run("an undecided round pays nobody", func(t *testing.T) {
		w := newWorld(t)
		w.tickets.fund = math.NewIntFromUint64(reward * 10)
		w.play(t, 1, [3]uint64{pass, fail, absent})
		for _, p := range w.p {
			requireInt(t, 0, w.balance(p.addr))
		}
		requireInt(t, reward*10, w.tickets.fund)
	})
	t.Run("a sealed report that was never opened is not paid", func(t *testing.T) {
		w := newWorld(t)
		w.tickets.fund = math.NewIntFromUint64(reward * 10)
		w.play(t, 1, [3]uint64{pass, pass, sealed})
		requireInt(t, 0, w.balance(w.p[2].addr))
		requireInt(t, reward*8, w.tickets.fund)
	})
	t.Run("an empty fund pays nothing and the tally still works", func(t *testing.T) {
		w := newWorld(t)
		w.play(t, 1, allPass)
		require.True(t, w.record(t, 1).Verified)
		for _, p := range w.p {
			requireInt(t, 0, w.balance(p.addr))
			require.Equal(t, uint64(1), w.prober(t, p.id).Accepted)
		}
		require.False(t, w.hasEvent("verify_reward"))
	})
	t.Run("a fund that runs out pays what it has", func(t *testing.T) {
		w := newWorld(t)
		w.tickets.fund = math.NewIntFromUint64(reward + reward/2)
		w.play(t, 1, allPass)
		total := math.ZeroInt()
		for _, p := range w.p {
			total = total.Add(w.balance(p.addr))
		}
		requireInt(t, reward+reward/2, total)
		require.True(t, w.tickets.fund.IsZero())
		require.True(t, w.record(t, 1).Verified)
	})
	t.Run("a reward of zero switches payments off", func(t *testing.T) {
		w := newWorld(t)
		w.setParams(t, func(p *types.Params) { p.ReportReward = 0 })
		w.tickets.fund = math.NewIntFromUint64(reward * 10)
		w.play(t, 1, allPass)
		requireInt(t, reward*10, w.tickets.fund)
	})
	t.Run("failed nodes still earn their probers a reward", func(t *testing.T) {
		w := newWorld(t)
		w.tickets.fund = math.NewIntFromUint64(reward * 10)
		w.play(t, 1, allFail)
		for _, p := range w.p {
			requireInt(t, reward, w.balance(p.addr))
		}
	})
	t.Run("a failing payment does not undo the tally", func(t *testing.T) {
		w := newWorld(t)
		w.tickets.fund = math.NewIntFromUint64(reward * 10)
		w.tickets.failPay = true
		w.play(t, 1, allPass)
		require.True(t, w.record(t, 1).Verified)
		require.Equal(t, uint64(1), w.prober(t, w.p[0].id).Accepted)
		require.True(t, w.hasEvent("verify_reward_failed"))
		require.False(t, w.hasEvent("verify_tally_failed"))
	})
}
