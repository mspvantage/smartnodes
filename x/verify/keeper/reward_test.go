package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/stretchr/testify/require"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/verify/keeper"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

// Reward tests use round numbers that are easy to check by hand: a year of
// 100 blocks and a round of 10, so each round is a tenth of a year. With the
// default 20% yearly release, a round releases 2% of what the pool has free:
// from a pool of 1,000,000 that is 20,000, of which 16,000 (80%) is the node
// budget and 4,000 goes to the prober fund.
const (
	testPool       = uint64(1_000_000)
	firstRelease   = uint64(20_000)
	firstBudget    = uint64(16_000)
	firstProberCut = uint64(4_000)
)

// newRewardWorld is a world whose pool is funded during round 1, so round 2
// is the first round with a budget. It returns in round 2.
func newRewardWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.setParams(t, func(p *types.Params) { p.BlocksPerYear = 100 })
	w.fundPool(testPool)
	w.nextRound(t)
	require.Equal(t, uint64(2), w.round(t))
	return w
}

func (w *world) budget(t *testing.T, round uint64) uint64 {
	t.Helper()
	b, err := w.keeper.RoundBudget.Get(w.ctx, round)
	if err != nil {
		return 0
	}
	return b
}

func (w *world) operatorAddr() []byte {
	addr, _ := w.addressCodec.StringToBytes(w.operator())
	return addr
}

func (w *world) operatorBalance() math.Int {
	return w.balance(w.operatorAddr())
}

// claim sends a claim for real and returns what it paid.
func (w *world) claim(t *testing.T, nodeID uint64) uint64 {
	t.Helper()
	res, err := w.ms.ClaimNodeReward(w.ctx, &types.MsgClaimNodeReward{Operator: w.operator(), NodeId: nodeID})
	require.NoError(t, err)
	return res.Amount
}

func (w *world) claimErr(nodeID uint64) error {
	_, err := w.ms.ClaimNodeReward(w.scratch(), &types.MsgClaimNodeReward{Operator: w.operator(), NodeId: nodeID})
	return err
}

func TestRewardCapPerPass(t *testing.T) {
	p := types.DefaultParams()
	// An entry node with a 1,000 SNODE minimum bond, probed every hour, can
	// earn at most 0.034 SNODE per pass: 30% of the bond over a year.
	require.Equal(t, uint64(34_223), keeper.RewardCapPerPass(p, 1_000_000_000))
	require.Equal(t, uint64(171_115), keeper.RewardCapPerPass(p, 5_000_000_000))
	perYear := keeper.RewardCapPerPass(p, 1_000_000_000) * (p.BlocksPerYear / p.RoundBlocks)
	require.InDelta(t, 300_000_000, perYear, 1_000_000, "a year of passes should add up to about 300 SNODE")

	// Probed one round in four, each pass is worth four times as much.
	p.SampleBps = 2_500
	require.Equal(t, uint64(136_892), keeper.RewardCapPerPass(p, 1_000_000_000))

	// Degenerate settings give no reward instead of dividing by zero.
	p.SampleBps = 0
	require.Zero(t, keeper.RewardCapPerPass(p, 1_000_000_000))
	p = types.DefaultParams()
	p.BlocksPerYear = 0
	require.Zero(t, keeper.RewardCapPerPass(p, 1_000_000_000))
	p = types.DefaultParams()
	p.RewardCapBpsPerYear = 0
	require.Zero(t, keeper.RewardCapPerPass(p, 1_000_000_000))
}

func TestRewardBudgetAtRoundStart(t *testing.T) {
	w := newRewardWorld(t)

	// Round 1 started before the pool was funded: no budget.
	require.Zero(t, w.budget(t, 1))
	// Round 2: 2% of the pool is released.
	require.Equal(t, firstBudget, w.budget(t, 2))
	requireInt(t, firstProberCut, w.tickets.fund)
	// Only the prober fund's part has left the pool. The node budget stays
	// until it is earned and claimed.
	requireInt(t, testPool-firstProberCut, w.poolBalance())
	require.True(t, w.hasEvent("verify_reward_budget"))

	res, err := w.qs.RewardPool(w.ctx, &types.QueryRewardPoolRequest{})
	require.NoError(t, err)
	require.Equal(t, testPool-firstProberCut, res.Balance)
	require.Zero(t, res.Owed)
	require.Equal(t, firstBudget, res.RoundBudget)
	require.NotEmpty(t, res.Address)

	// Each later round releases 2% of what is then left, so budgets shrink.
	w.nextRound(t)
	left := testPool - firstProberCut
	wantRelease := left * 2 / 100
	wantBudget := wantRelease * 80 / 100
	require.Equal(t, wantBudget, w.budget(t, 3))
	require.Less(t, w.budget(t, 3), w.budget(t, 2))
	requireInt(t, left-(wantRelease-wantBudget), w.poolBalance())
	w.requirePoolSolvent(t)
}

func TestNoPoolNoReward(t *testing.T) {
	w := newWorld(t)
	w.play(t, 1, allPass)
	rec := w.record(t, 1)
	require.True(t, rec.Verified)
	require.Zero(t, rec.PendingRound)
	require.Zero(t, rec.RewardBalance)
	require.ErrorIs(t, w.claimErr(1), types.ErrNothingToClaim)
	require.False(t, w.hasEvent("verify_reward_budget"))
}

// One node passes: it earns the whole node budget of that round, and the
// operator can claim it.
func TestEarnAndClaim(t *testing.T) {
	w := newRewardWorld(t)
	round := w.play(t, 1, allPass)
	require.Equal(t, uint64(2), round)

	rec := w.record(t, 1)
	require.Equal(t, uint64(2), rec.PendingRound)
	require.NotZero(t, rec.PendingWeight)
	require.Zero(t, rec.RewardBalance, "the share is fixed later, not at the tally")
	w.requirePoolSolvent(t)

	poolBefore := w.poolBalance()
	paid := w.claim(t, 1)
	require.Equal(t, firstBudget, paid)
	requireInt(t, firstBudget, w.operatorBalance())
	requireInt(t, 0, poolBefore.Sub(w.poolBalance()).Sub(math.NewIntFromUint64(firstBudget)))
	require.True(t, w.hasEvent("verify_claim_node_reward"))

	rec = w.record(t, 1)
	require.Zero(t, rec.RewardBalance)
	require.Zero(t, rec.PendingRound)
	require.Equal(t, firstBudget, rec.RewardPaid)
	w.requirePoolSolvent(t)

	// Nothing more to claim.
	require.ErrorIs(t, w.claimErr(1), types.ErrNothingToClaim)
	// Prober bonds were never involved.
	w.requireBondsExact(t)
}

func TestClaimRejected(t *testing.T) {
	w := newRewardWorld(t)
	w.play(t, 1, allPass)

	t.Run("not the operator", func(t *testing.T) {
		_, err := w.ms.ClaimNodeReward(w.scratch(), &types.MsgClaimNodeReward{Operator: w.p[0].str, NodeId: 1})
		require.ErrorIs(t, err, types.ErrNotOperator)
	})
	t.Run("invalid address", func(t *testing.T) {
		_, err := w.ms.ClaimNodeReward(w.scratch(), &types.MsgClaimNodeReward{Operator: "nope", NodeId: 1})
		require.Error(t, err)
	})
	t.Run("unknown node", func(t *testing.T) {
		require.ErrorIs(t, w.claimErr(99), registrytypes.ErrNodeNotFound)
	})
	t.Run("node that never earned", func(t *testing.T) {
		require.ErrorIs(t, w.claimErr(2), types.ErrNothingToClaim)
	})
	// None of the failed attempts moved any coins.
	requireInt(t, 0, w.operatorBalance())
}

// Two nodes of the same tier pass the same round: they split the budget
// equally, and together never get more than the budget.
func TestTwoNodesShareTheBudget(t *testing.T) {
	w := newRewardWorld(t)
	for _, p := range w.p {
		w.commit(t, p, 2, 1, pass, 0)
		w.commit(t, p, 2, 2, pass, 0)
	}
	w.nextRound(t)
	for _, p := range w.p {
		w.reveal(t, p, 2, 1, pass, 0)
		w.reveal(t, p, 2, 2, pass, 0)
	}
	w.nextRound(t)

	a := w.claim(t, 1)
	b := w.claim(t, 2)
	require.Equal(t, firstBudget/2, a)
	require.Equal(t, firstBudget/2, b)
	require.LessOrEqual(t, a+b, firstBudget)
	w.requirePoolSolvent(t)
}

// An exit node, with five times the minimum bond, earns five times the share
// of an entry node.
func TestShareFollowsTier(t *testing.T) {
	w := newRewardWorld(t)
	node := w.registry.nodes[2]
	node.Tier = registrytypes.TierExit
	w.registry.nodes[2] = node

	for _, p := range w.p {
		w.commit(t, p, 2, 1, pass, 0)
		w.commit(t, p, 2, 2, pass, 0)
	}
	w.nextRound(t)
	for _, p := range w.p {
		w.reveal(t, p, 2, 1, pass, 0)
		w.reveal(t, p, 2, 2, pass, 0)
	}
	w.nextRound(t)

	entry := w.claim(t, 1)
	exit := w.claim(t, 2)
	require.Equal(t, firstBudget/6, entry)  // 2,666
	require.Equal(t, firstBudget*5/6, exit) // 13,333
	require.LessOrEqual(t, entry+exit, firstBudget)
	w.requirePoolSolvent(t)
}

// The cap limits what one node can earn however large the budget is. What
// the cap holds back stays in the pool.
func TestCapLimitsTheShare(t *testing.T) {
	w := newRewardWorld(t)
	// With this cap an entry node may earn at most 10,000 per pass, below
	// the 16,000 budget.
	w.setParams(t, func(p *types.Params) { p.RewardCapBpsPerYear = 1 })
	params, err := w.keeper.Params.Get(w.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(10_000), keeper.RewardCapPerPass(params, nodeBond))

	w.play(t, 1, allPass)
	poolBefore := w.poolBalance()
	require.Equal(t, uint64(10_000), w.claim(t, 1))
	requireInt(t, 10_000, poolBefore.Sub(w.poolBalance()))
	w.requirePoolSolvent(t)
}

// Only a node that passes, and is not jailed, earns.
func TestOnlyPassingNodesEarn(t *testing.T) {
	for name, results := range map[string][3]uint64{
		"failed":    allFail,
		"tampering": allTamper,
		"undecided": {pass, fail, absent},
		"one pass":  {pass, absent, absent},
	} {
		t.Run(name, func(t *testing.T) {
			w := newRewardWorld(t)
			w.play(t, 1, results)
			rec := w.record(t, 1)
			require.Zero(t, rec.PendingRound)
			require.Zero(t, rec.RewardBalance)
			require.ErrorIs(t, w.claimErr(1), types.ErrNothingToClaim)
			w.requirePoolSolvent(t)
		})
	}
}

// The share of an earlier round is fixed automatically the next time the
// node is tallied, with no action from the operator.
func TestShareIsFixedAtTheNextTally(t *testing.T) {
	w := newRewardWorld(t)
	first := w.play(t, 1, allPass)  // earns in round 2
	second := w.play(t, 1, allPass) // earns in round 4; fixes round 2
	require.Equal(t, uint64(2), first)
	require.Equal(t, uint64(4), second)

	rec := w.record(t, 1)
	require.Equal(t, firstBudget, rec.RewardBalance)
	require.Equal(t, second, rec.PendingRound)
	w.requirePoolSolvent(t)

	// A failed round fixes the pending share too, and earns nothing itself.
	w.play(t, 1, allFail)
	rec = w.record(t, 1)
	require.Zero(t, rec.PendingRound)
	require.Equal(t, firstBudget+w.budget(t, second), rec.RewardBalance)
	w.requirePoolSolvent(t)

	// One claim pays everything that built up.
	require.Equal(t, firstBudget+w.budget(t, second), w.claim(t, 1))
	w.requirePoolSolvent(t)
}

// What a node is owed is set aside: later rounds only release from what the
// pool has free.
func TestOwedCoinsAreNotReleasedAgain(t *testing.T) {
	w := newRewardWorld(t)
	w.play(t, 1, allPass)
	w.play(t, 1, allPass) // round 2's 16,000 is now owed to node 1
	require.Equal(t, firstBudget, w.record(t, 1).RewardBalance)

	// The next round's release is 2% of the balance minus what is owed.
	balance := w.poolBalance()
	w.nextRound(t)
	free := balance.Sub(math.NewIntFromUint64(firstBudget))
	wantBudget := free.MulRaw(2).QuoRaw(100).MulRaw(80).QuoRaw(100)
	requireInt(t, w.budget(t, w.round(t)), wantBudget)
	w.requirePoolSolvent(t)
}

// A balance that has been fixed never expires, however long the operator
// waits. Only a share that was never fixed can be lost.
func TestFixedBalanceNeverExpires(t *testing.T) {
	w := newRewardWorld(t)
	// Budgets are kept for six rounds: long enough for round 2's share to
	// be fixed at the node's next tally, in round 6.
	w.setParams(t, func(p *types.Params) { p.RewardRetentionRounds = 6 })
	w.play(t, 1, allPass)           // earns in round 2
	second := w.play(t, 1, allPass) // earns in round 4; fixes round 2

	// The node is not probed again and the operator waits far longer than
	// the retention time.
	for i := 0; i < 12; i++ {
		w.nextRound(t)
	}
	require.Zero(t, w.budget(t, 2), "old budgets are deleted")
	require.Zero(t, w.budget(t, second))

	// The fixed balance is still paid in full. The share of the last round,
	// which was never fixed, is forfeited and stays in the pool.
	require.Equal(t, firstBudget, w.claim(t, 1))
	rec := w.record(t, 1)
	require.Zero(t, rec.PendingRound)
	require.Zero(t, rec.RewardBalance)
	w.requirePoolSolvent(t)
}

// A share that is never fixed is forfeited once its round's budget has been
// deleted.
func TestUnfixedShareIsForfeited(t *testing.T) {
	w := newRewardWorld(t)
	w.setParams(t, func(p *types.Params) { p.RewardRetentionRounds = 4 })
	w.play(t, 1, allPass)
	for i := 0; i < 8; i++ {
		w.nextRound(t)
	}
	require.ErrorIs(t, w.claimErr(1), types.ErrNothingToClaim)
	requireInt(t, 0, w.operatorBalance())
	w.requirePoolSolvent(t)
}

// A claim made before the node's last round has been fully counted pays what
// is already fixed and leaves that round pending.
func TestClaimLeavesUncountedRoundPending(t *testing.T) {
	w := newRewardWorld(t)
	w.play(t, 1, allPass)
	w.play(t, 1, allPass) // balance 16,000, pending round 4

	// Pretend the node's pending round is the current one, which cannot
	// have been counted yet.
	rec := w.record(t, 1)
	current := w.round(t)
	rec.PendingRound = current
	require.NoError(t, w.keeper.NodeRecord.Set(w.ctx, 1, rec))

	require.Equal(t, firstBudget, w.claim(t, 1))
	rec = w.record(t, 1)
	require.Equal(t, current, rec.PendingRound, "the uncounted round must stay pending")
	require.Zero(t, rec.RewardBalance)
}

// The pool can never pay out more than it has free, whatever the budget
// records say.
func TestShareIsCappedByFreeBalance(t *testing.T) {
	w := newRewardWorld(t)
	w.play(t, 1, allPass)

	// Corrupt the budget to far more than the pool holds.
	require.NoError(t, w.keeper.RoundBudget.Set(w.ctx, 2, 10_000_000_000))
	pool := w.poolBalance()
	paid := w.claim(t, 1)
	require.True(t, math.NewIntFromUint64(paid).LTE(pool), "paid %d from a pool of %s", paid, pool)
	require.False(t, w.poolBalance().IsNegative())
	w.requirePoolSolvent(t)
}

// When a node leaves the registry, a reward it never claimed goes back to
// the pool's free balance.
func TestUnclaimedRewardReturnsWhenNodeLeaves(t *testing.T) {
	w := newRewardWorld(t)
	w.play(t, 1, allPass)
	w.play(t, 1, allPass)
	require.Equal(t, firstBudget, w.record(t, 1).RewardBalance)

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
	owed, err := w.keeper.RewardOwed.Get(w.ctx)
	require.NoError(t, err)
	require.Zero(t, owed)
	w.requirePoolSolvent(t)
}

func TestFundNodeRewards(t *testing.T) {
	f := initFixture(t)
	addr, str := f.account(t, 1)
	f.fund(addr, 5_000)

	_, err := f.ms.FundNodeRewards(f.ctx, &types.MsgFundNodeRewards{Sender: str, Amount: 3_000})
	require.NoError(t, err)
	requireInt(t, 3_000, f.poolBalance())
	requireInt(t, 2_000, f.balance(addr))
	require.True(t, f.hasEvent("verify_fund_node_rewards"))

	_, err = f.ms.FundNodeRewards(f.scratch(), &types.MsgFundNodeRewards{Sender: str, Amount: 0})
	require.ErrorIs(t, err, types.ErrInvalidAmount)
	_, err = f.ms.FundNodeRewards(f.scratch(), &types.MsgFundNodeRewards{Sender: str, Amount: 2_001})
	require.ErrorIs(t, err, sdkerrors.ErrInsufficientFunds)
	_, err = f.ms.FundNodeRewards(f.scratch(), &types.MsgFundNodeRewards{Sender: "nope", Amount: 1})
	require.Error(t, err)

	// Funding never touches prober bonds.
	requireInt(t, 0, f.moduleBalance())
}

// A fault while setting a reward budget must not stop the round starting.
func TestBudgetFailureDoesNotStopTheRound(t *testing.T) {
	w := newWorld(t)
	w.setParams(t, func(p *types.Params) { p.BlocksPerYear = 100 })
	w.fundPool(testPool)
	w.tickets.failFund = true

	w.nextRound(t)
	require.Equal(t, uint64(2), w.round(t))
	require.True(t, w.hasEvent("verify_reward_budget_failed"))
	require.False(t, w.hasEvent("verify_begin_block_failed"))
	require.Zero(t, w.budget(t, 2))
	// Nothing half-done: the pool is untouched.
	requireInt(t, testPool, w.poolBalance())

	// The round works normally, without rewards.
	w.play(t, 1, allPass)
	require.True(t, w.record(t, 1).Verified)
}

// Settings that switch rewards off.
func TestRewardSwitches(t *testing.T) {
	t.Run("zero release rate", func(t *testing.T) {
		w := newWorld(t)
		w.setParams(t, func(p *types.Params) { p.BlocksPerYear = 100; p.PoolReleaseBpsPerYear = 0 })
		w.fundPool(testPool)
		w.nextRound(t)
		require.Zero(t, w.budget(t, 2))
		requireInt(t, testPool, w.poolBalance())
	})
	t.Run("everything to nodes", func(t *testing.T) {
		w := newWorld(t)
		w.setParams(t, func(p *types.Params) { p.BlocksPerYear = 100; p.NodeShareBps = types.BpsDenominator })
		w.fundPool(testPool)
		w.nextRound(t)
		require.Equal(t, firstRelease, w.budget(t, 2))
		require.True(t, w.tickets.fund.IsZero())
	})
	t.Run("everything to probers", func(t *testing.T) {
		w := newWorld(t)
		w.setParams(t, func(p *types.Params) { p.BlocksPerYear = 100; p.NodeShareBps = 0 })
		w.fundPool(testPool)
		w.nextRound(t)
		require.Zero(t, w.budget(t, 2))
		requireInt(t, firstRelease, w.tickets.fund)
	})
}

// Importing genesis keeps fixed balances, rebuilds the total owed from them
// and drops shares that were still pending.
func TestGenesisKeepsRewardBalances(t *testing.T) {
	f := initFixture(t)
	gen := types.DefaultGenesis()
	gen.NodeRecordList = []types.NodeRecord{
		{NodeId: 1, RewardBalance: 700, PendingRound: 9, PendingWeight: 5},
		{NodeId: 2, RewardBalance: 300, RewardPaid: 50},
	}
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *gen))

	owed, err := f.keeper.RewardOwed.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1_000), owed)
	rec, err := f.keeper.NodeRecord.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(700), rec.RewardBalance)
	require.Zero(t, rec.PendingRound)
	require.Zero(t, rec.PendingWeight)

	f.fundPool(1_000)
	f.requirePoolSolvent(t)
}
