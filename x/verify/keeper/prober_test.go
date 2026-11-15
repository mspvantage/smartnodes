package keeper_test

import (
	"testing"
	"time"

	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

var (
	minBond   = types.DefaultMinProberBond
	unbonding = time.Duration(types.DefaultUnbondingSeconds) * time.Second
)

func TestRegisterProber(t *testing.T) {
	f := initFixture(t)
	addr, str := f.account(t, 1)
	f.fund(addr, minBond+500)

	res, err := f.ms.RegisterProber(f.ctx, &types.MsgRegisterProber{Owner: str, Bond: minBond})
	require.NoError(t, err)

	p := f.prober(t, res.Id)
	require.Equal(t, str, p.Owner)
	require.Equal(t, minBond, p.Bond)
	require.Equal(t, types.ProberPending, p.Status)
	require.Zero(t, p.UnbondingEnd)

	// The bond left the owner's account and sits in the module account.
	requireInt(t, 500, f.balance(addr))
	requireInt(t, minBond, f.moduleBalance())
	f.requireBondsExact(t)

	// The owner index and the waiting list were filled in.
	id, err := f.keeper.ProberByOwner.Get(f.ctx, str)
	require.NoError(t, err)
	require.Equal(t, res.Id, id)
	waiting, err := f.keeper.PendingJoin.Has(f.ctx, res.Id)
	require.NoError(t, err)
	require.True(t, waiting)
	require.True(t, f.hasEvent("verify_register_prober"))

	// Ids count up.
	second := f.newProber(t, 2)
	require.Equal(t, res.Id+1, second.id)
	f.requireBondsExact(t)
}

func TestRegisterProberRejected(t *testing.T) {
	f := initFixture(t)
	addr, str := f.account(t, 1)
	f.fund(addr, minBond*3)

	t.Run("invalid address", func(t *testing.T) {
		_, err := f.ms.RegisterProber(f.scratch(), &types.MsgRegisterProber{Owner: "not-an-address", Bond: minBond})
		require.Error(t, err)
	})
	t.Run("bond below the minimum", func(t *testing.T) {
		_, err := f.ms.RegisterProber(f.scratch(), &types.MsgRegisterProber{Owner: str, Bond: minBond - 1})
		require.ErrorIs(t, err, types.ErrBondTooLow)
	})
	t.Run("zero bond", func(t *testing.T) {
		_, err := f.ms.RegisterProber(f.scratch(), &types.MsgRegisterProber{Owner: str})
		require.ErrorIs(t, err, types.ErrBondTooLow)
	})
	t.Run("cannot pay", func(t *testing.T) {
		_, poor := f.account(t, 9)
		_, err := f.ms.RegisterProber(f.ctx, &types.MsgRegisterProber{Owner: poor, Bond: minBond})
		require.ErrorIs(t, err, sdkerrors.ErrInsufficientFunds)
		// Nothing was stored for the failed registration.
		has, err := f.keeper.ProberByOwner.Has(f.ctx, poor)
		require.NoError(t, err)
		require.False(t, has)
		requireInt(t, 0, f.moduleBalance())
	})
	t.Run("second prober for the same address", func(t *testing.T) {
		_, err := f.ms.RegisterProber(f.ctx, &types.MsgRegisterProber{Owner: str, Bond: minBond})
		require.NoError(t, err)
		_, err = f.ms.RegisterProber(f.scratch(), &types.MsgRegisterProber{Owner: str, Bond: minBond})
		require.ErrorIs(t, err, types.ErrAlreadyProber)
		// Only one bond was taken.
		requireInt(t, minBond, f.moduleBalance())
		f.requireBondsExact(t)
	})
}

func TestBondAboveMinimumIsKeptWhole(t *testing.T) {
	f := initFixture(t)
	addr, str := f.account(t, 1)
	f.fund(addr, minBond*2)
	res, err := f.ms.RegisterProber(f.ctx, &types.MsgRegisterProber{Owner: str, Bond: minBond * 2})
	require.NoError(t, err)
	require.Equal(t, minBond*2, f.prober(t, res.Id).Bond)
	requireInt(t, 0, f.balance(addr))
	f.requireBondsExact(t)
}

// A prober joins the active set only when a round starts, never mid-round.
func TestProberJoinsAtRoundStart(t *testing.T) {
	f := initFixture(t)
	f.begin(t) // round 1 starts with nobody
	require.Equal(t, uint64(1), f.round(t))

	p := f.newProber(t, 1)
	f.nextBlocks(t, testRoundBlocks-1) // still round 1
	require.Equal(t, uint64(1), f.round(t))
	require.Equal(t, types.ProberPending, f.prober(t, p.id).Status)
	f.requireActiveConsistent(t)

	f.nextBlocks(t, 1) // round 2 starts
	require.Equal(t, uint64(2), f.round(t))
	require.Equal(t, types.ProberActive, f.prober(t, p.id).Status)
	waiting, err := f.keeper.PendingJoin.Has(f.ctx, p.id)
	require.NoError(t, err)
	require.False(t, waiting)
	require.True(t, f.hasEvent("verify_prober_joined"))
	f.requireActiveConsistent(t)
}

// A prober that has not joined yet can leave at once.
func TestDeregisterPendingProber(t *testing.T) {
	f := initFixture(t)
	f.begin(t)
	p := f.newProber(t, 1)

	_, err := f.ms.DeregisterProber(f.ctx, &types.MsgDeregisterProber{Owner: p.str})
	require.NoError(t, err)
	got := f.prober(t, p.id)
	require.Equal(t, types.ProberUnbonding, got.Status)
	require.Equal(t, f.sdkCtx.BlockTime().Unix()+int64(types.DefaultUnbondingSeconds), got.UnbondingEnd)

	// It never joins the active set.
	f.nextRound(t)
	require.Equal(t, types.ProberUnbonding, f.prober(t, p.id).Status)
	f.requireActiveConsistent(t)

	// The bond is locked for the whole unbonding period.
	_, err = f.ms.WithdrawProberBond(f.scratch(), &types.MsgWithdrawProberBond{Owner: p.str})
	require.ErrorIs(t, err, types.ErrUnbondingNotOver)
	f.requireBondsExact(t)
}

// An active prober stays assignable until the next round starts.
func TestDeregisterActiveProber(t *testing.T) {
	w := newWorld(t)
	p := w.p[1]

	_, err := w.ms.DeregisterProber(w.ctx, &types.MsgDeregisterProber{Owner: p.str})
	require.NoError(t, err)
	require.Equal(t, types.ProberLeaving, w.prober(t, p.id).Status)
	w.requireActiveConsistent(t) // still in the list

	// It can still report in the round it is leaving.
	w.commit(t, p, 1, 1, types.ResultPass, 0)

	// Asking twice is refused.
	_, err = w.ms.DeregisterProber(w.scratch(), &types.MsgDeregisterProber{Owner: p.str})
	require.ErrorIs(t, err, types.ErrProberLeaving)

	// Even with the unbonding time over, the bond stays locked while the
	// prober is still in the active set.
	w.advanceTime(unbonding + time.Hour)
	_, err = w.ms.WithdrawProberBond(w.scratch(), &types.MsgWithdrawProberBond{Owner: p.str})
	require.ErrorIs(t, err, types.ErrProberNotUnbonding)

	w.nextRound(t)
	require.Equal(t, types.ProberUnbonding, w.prober(t, p.id).Status)
	require.True(t, w.hasEvent("verify_prober_left"))
	w.requireActiveConsistent(t)
	count, err := w.keeper.ActiveCount.Get(w.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), count)

	// Out of the active set, it can no longer seal reports.
	_, err = w.ms.CommitReports(w.scratch(), &types.MsgCommitReports{
		Owner: p.str, Round: 2, NodeIds: []uint64{2}, Hashes: []string{seal(2, 2, p.id, types.ResultPass, 0)},
	})
	require.ErrorIs(t, err, types.ErrProberNotActive)

	// But it can still open what it sealed before leaving.
	w.reveal(t, p, 1, 1, types.ResultPass, 0)
}

func TestWithdrawProberBond(t *testing.T) {
	w := newWorld(t)
	p := w.p[0]

	t.Run("active prober cannot withdraw", func(t *testing.T) {
		_, err := w.ms.WithdrawProberBond(w.scratch(), &types.MsgWithdrawProberBond{Owner: p.str})
		require.ErrorIs(t, err, types.ErrProberNotUnbonding)
	})
	t.Run("unknown address", func(t *testing.T) {
		_, stranger := w.account(t, 0x77)
		_, err := w.ms.WithdrawProberBond(w.scratch(), &types.MsgWithdrawProberBond{Owner: stranger})
		require.ErrorIs(t, err, types.ErrProberNotFound)
		_, err = w.ms.DeregisterProber(w.scratch(), &types.MsgDeregisterProber{Owner: stranger})
		require.ErrorIs(t, err, types.ErrProberNotFound)
	})

	_, err := w.ms.DeregisterProber(w.ctx, &types.MsgDeregisterProber{Owner: p.str})
	require.NoError(t, err)
	w.nextRound(t)

	t.Run("one second early", func(t *testing.T) {
		end := w.prober(t, p.id).UnbondingEnd
		w.advanceTime(time.Duration(end-w.sdkCtx.BlockTime().Unix()-1) * time.Second)
		_, err := w.ms.WithdrawProberBond(w.scratch(), &types.MsgWithdrawProberBond{Owner: p.str})
		require.ErrorIs(t, err, types.ErrUnbondingNotOver)
	})
	t.Run("at the end", func(t *testing.T) {
		w.advanceTime(time.Second)
		_, err := w.ms.WithdrawProberBond(w.ctx, &types.MsgWithdrawProberBond{Owner: p.str})
		require.NoError(t, err)

		requireInt(t, minBond, w.balance(p.addr))
		requireInt(t, minBond*2, w.moduleBalance())
		w.requireBondsExact(t)

		// The records are gone.
		has, err := w.keeper.Prober.Has(w.ctx, p.id)
		require.NoError(t, err)
		require.False(t, has)
		has, err = w.keeper.ProberByOwner.Has(w.ctx, p.str)
		require.NoError(t, err)
		require.False(t, has)
	})
	t.Run("cannot withdraw twice", func(t *testing.T) {
		_, err := w.ms.WithdrawProberBond(w.scratch(), &types.MsgWithdrawProberBond{Owner: p.str})
		require.ErrorIs(t, err, types.ErrProberNotFound)
	})
	t.Run("the address can register again", func(t *testing.T) {
		res, err := w.ms.RegisterProber(w.ctx, &types.MsgRegisterProber{Owner: p.str, Bond: minBond})
		require.NoError(t, err)
		require.NotEqual(t, p.id, res.Id, "ids are never reused")
		w.requireBondsExact(t)
	})
}

// The unbonding end is fixed when the prober asks to leave. A later change
// to the parameter must not move it.
func TestUnbondingPeriodIsFixedAtDeregister(t *testing.T) {
	w := newWorld(t)
	p := w.p[0]
	_, err := w.ms.DeregisterProber(w.ctx, &types.MsgDeregisterProber{Owner: p.str})
	require.NoError(t, err)
	end := w.prober(t, p.id).UnbondingEnd

	w.setParams(t, func(p *types.Params) { p.UnbondingSeconds = 60 })
	w.nextRound(t)
	require.Equal(t, end, w.prober(t, p.id).UnbondingEnd)

	w.advanceTime(time.Hour)
	_, err = w.ms.WithdrawProberBond(w.scratch(), &types.MsgWithdrawProberBond{Owner: p.str})
	require.ErrorIs(t, err, types.ErrUnbondingNotOver)
}

// Removing a prober from the middle of the active list must leave the list
// dense and the index correct, whichever prober leaves.
func TestActiveListStaysDense(t *testing.T) {
	for leaver := 0; leaver < 5; leaver++ {
		f := initFixture(t)
		var ps []*tester
		for i := 0; i < 5; i++ {
			ps = append(ps, f.newProber(t, byte(0x20+i)))
		}
		f.begin(t)
		f.requireActiveConsistent(t)

		_, err := f.ms.DeregisterProber(f.ctx, &types.MsgDeregisterProber{Owner: ps[leaver].str})
		require.NoError(t, err)
		f.nextRound(t)
		f.requireActiveConsistent(t)

		count, err := f.keeper.ActiveCount.Get(f.ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(4), count)
	}
}

// Probers joining and leaving in the same round start are both applied.
func TestJoinAndLeaveTogether(t *testing.T) {
	w := newWorld(t)
	_, err := w.ms.DeregisterProber(w.ctx, &types.MsgDeregisterProber{Owner: w.p[0].str})
	require.NoError(t, err)
	_, err = w.ms.DeregisterProber(w.ctx, &types.MsgDeregisterProber{Owner: w.p[2].str})
	require.NoError(t, err)
	a := w.newProber(t, 0x41)
	b := w.newProber(t, 0x42)

	w.nextRound(t)
	w.requireActiveConsistent(t)
	require.Equal(t, types.ProberActive, w.prober(t, a.id).Status)
	require.Equal(t, types.ProberActive, w.prober(t, b.id).Status)
	require.Equal(t, types.ProberUnbonding, w.prober(t, w.p[0].id).Status)
	require.Equal(t, types.ProberActive, w.prober(t, w.p[1].id).Status)
	count, err := w.keeper.ActiveCount.Get(w.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), count)
	w.requireBondsExact(t)
}

func TestUnjailIsNotAvailableYet(t *testing.T) {
	f := initFixture(t)
	_, str := f.account(t, 1)
	_, err := f.ms.Unjail(f.ctx, &types.MsgUnjail{Operator: str, NodeId: 1})
	require.ErrorIs(t, err, types.ErrJailingNotAvailable)
	_, err = f.ms.Unjail(f.ctx, &types.MsgUnjail{Operator: "bad", NodeId: 1})
	require.Error(t, err)
}
