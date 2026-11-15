package keeper_test

import (
	"fmt"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/mspvantage/smartnodes/x/rewards/keeper"
	"github.com/mspvantage/smartnodes/x/rewards/types"
)

const denom = "usnode"

func coins(amount int64) sdk.Coins { return sdk.NewCoins(sdk.NewInt64Coin(denom, amount)) }

// validatorSet registers n validators with the given voting powers and
// returns the votes of a block they all took part in.
func (f *fixture) validatorSet(powers ...int64) []abci.VoteInfo {
	votes := make([]abci.VoteInfo, len(powers))
	for i, power := range powers {
		cons := []byte(fmt.Sprintf("cons-address-%02d......", i))[:20]
		f.staking.byCons[string(cons)] = stakingtypes.Validator{OperatorAddress: fmt.Sprintf("validator-%d", i)}
		votes[i] = abci.VoteInfo{Validator: abci.Validator{Address: cons, Power: power}}
	}
	return votes
}

func (f *fixture) collect(amount sdk.Coins) {
	addr := moduleAddr(authtypes.FeeCollectorName)
	f.bank.balances[string(addr)] = f.bank.balances[string(addr)].Add(amount...)
}

func (f *fixture) feeCollector() math.Int {
	return f.bank.balances[string(moduleAddr(authtypes.FeeCollectorName))].AmountOf(denom)
}

func (f *fixture) distribution() math.Int {
	return f.bank.balances[string(moduleAddr(distrtypes.ModuleName))].AmountOf(denom)
}

// credited returns what validator i was credited, in whole units.
func (f *fixture) credited(i int) math.Int {
	return f.distr.credited[fmt.Sprintf("validator-%d", i)].AmountOf(denom).TruncateInt()
}

func requireInt(t *testing.T, want int64, got math.Int) {
	t.Helper()
	require.True(t, math.NewInt(want).Equal(got), "want %d, got %s", want, got)
}

// requireConserved checks that no coin was created or lost: the fee collector
// and the distribution module together still hold the original amount, and
// the distribution module holds exactly what was credited plus the community pool.
func (f *fixture) requireConserved(t *testing.T, original int64, validators int) {
	t.Helper()
	requireInt(t, original, f.feeCollector().Add(f.distribution()))
	creditedTotal := math.ZeroInt()
	for i := 0; i < validators; i++ {
		creditedTotal = creditedTotal.Add(f.credited(i))
	}
	require.True(t, f.distribution().Equal(creditedTotal.Add(f.distr.communityPool.AmountOf(denom))),
		"distribution module holds %s but %s was credited and %s went to the community pool",
		f.distribution(), creditedTotal, f.distr.communityPool.AmountOf(denom))
}

func TestEqualShareIgnoresVotingPower(t *testing.T) {
	f := initFixture(t)
	// one validator holds 97% of the power; the other three hold 1% each
	votes := f.validatorSet(97, 1, 1, 1)
	f.collect(coins(1_000_000))

	require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, votes))

	// half is paid equally: 500,000, less 2% community tax, split four ways
	for i := 0; i < 4; i++ {
		requireInt(t, 122_500, f.credited(i))
	}
	requireInt(t, 10_000, f.distr.communityPool.AmountOf(denom))
	// the other half is untouched, left for the standard by-stake payout
	requireInt(t, 500_000, f.feeCollector())
	requireInt(t, 500_000, f.distribution())
	f.requireConserved(t, 1_000_000, 4)
}

func TestEqualShareRoundingLosesNothing(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(5, 5, 5)
	f.collect(coins(1_000_001))

	require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, votes))

	// equal part 500,000 (rounded down from 500,000.5); tax 10,000;
	// 490,000 / 3 = 163,333 each, with 1 left over for the community pool
	for i := 0; i < 3; i++ {
		requireInt(t, 163_333, f.credited(i))
	}
	requireInt(t, 10_001, f.distr.communityPool.AmountOf(denom))
	requireInt(t, 500_001, f.feeCollector())
	f.requireConserved(t, 1_000_001, 3)
}

func TestEqualShareSettings(t *testing.T) {
	t.Run("zero turns it off", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.Params.Set(f.ctx, types.NewParams(0)))
		f.collect(coins(1_000_000))
		require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, f.validatorSet(1, 1)))
		requireInt(t, 1_000_000, f.feeCollector())
		require.True(t, f.distribution().IsZero())
	})

	t.Run("10,000 pays everything equally", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.Params.Set(f.ctx, types.NewParams(types.BasisPoints)))
		f.collect(coins(1_000_000))
		require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, f.validatorSet(9, 1)))
		requireInt(t, 490_000, f.credited(0))
		requireInt(t, 490_000, f.credited(1))
		require.True(t, f.feeCollector().IsZero())
		f.requireConserved(t, 1_000_000, 2)
	})

	t.Run("a quarter", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.Params.Set(f.ctx, types.NewParams(2_500)))
		f.collect(coins(1_000_000))
		require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, f.validatorSet(9, 1)))
		requireInt(t, 122_500, f.credited(0)) // 250,000 less 2%, halved
		requireInt(t, 750_000, f.feeCollector())
		f.requireConserved(t, 1_000_000, 2)
	})

	t.Run("no community tax", func(t *testing.T) {
		f := initFixture(t)
		f.distr.communityTax = math.LegacyZeroDec()
		f.collect(coins(1_000_000))
		require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, f.validatorSet(1, 1)))
		requireInt(t, 250_000, f.credited(0))
		require.True(t, f.distr.communityPool.IsZero())
		f.requireConserved(t, 1_000_000, 2)
	})
}

func TestEqualShareDoesNothingWhenThereIsNothingToDo(t *testing.T) {
	t.Run("empty fee collector", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, f.validatorSet(1, 1)))
		require.True(t, f.distribution().IsZero())
	})

	t.Run("no validators voted", func(t *testing.T) {
		f := initFixture(t)
		f.collect(coins(1_000_000))
		require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, nil))
		requireInt(t, 1_000_000, f.feeCollector())
	})

	t.Run("too little to give each validator one unit", func(t *testing.T) {
		f := initFixture(t)
		f.collect(coins(40)) // equal part 20, among 30 validators
		powers := make([]int64, 30)
		for i := range powers {
			powers[i] = 1
		}
		require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, f.validatorSet(powers...)))
		// left alone for the by-stake payout, not swept into the community pool
		requireInt(t, 40, f.feeCollector())
		require.True(t, f.distribution().IsZero())
		require.True(t, f.distr.communityPool.IsZero())
	})
}

func TestEqualShareSkipsValidatorsThatLeft(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(1, 1, 1)
	// a fourth vote from a validator the staking module no longer knows
	votes = append(votes, abci.VoteInfo{Validator: abci.Validator{Address: []byte("gone-validator-addr."), Power: 1}})
	f.collect(coins(1_200_000))

	require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, votes))

	// 600,000 less 2% = 588,000, shared among the three that remain
	for i := 0; i < 3; i++ {
		requireInt(t, 196_000, f.credited(i))
	}
	f.requireConserved(t, 1_200_000, 3)
}

func TestEqualShareHandlesSeveralDenoms(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(1, 3)
	f.collect(sdk.NewCoins(sdk.NewInt64Coin(denom, 1_000_000), sdk.NewInt64Coin("uusdc", 400)))

	require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, votes))

	requireInt(t, 245_000, f.credited(0))
	// 400 uusdc: equal part 200, tax 4, 196 / 2 = 98 each
	require.True(t, f.distr.credited["validator-0"].AmountOf("uusdc").TruncateInt().Equal(math.NewInt(98)))
	require.True(t, f.distr.credited["validator-1"].AmountOf("uusdc").TruncateInt().Equal(math.NewInt(98)))
	require.True(t, f.distr.communityPool.AmountOf("uusdc").Equal(math.NewInt(4)))
	collector := f.bank.balances[string(moduleAddr(authtypes.FeeCollectorName))]
	require.True(t, collector.AmountOf("uusdc").Equal(math.NewInt(200)))
	f.requireConserved(t, 1_000_000, 2)
}

func TestBeginBlockerSkipsTheFirstBlock(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(1, 1)
	f.collect(coins(1_000_000))

	ctx := f.sdkCtx.WithBlockHeight(1).WithVoteInfos(votes)
	require.NoError(t, f.keeper.BeginBlocker(ctx))
	requireInt(t, 1_000_000, f.feeCollector())

	ctx = f.sdkCtx.WithBlockHeight(2).WithVoteInfos(votes)
	require.NoError(t, f.keeper.BeginBlocker(ctx))
	requireInt(t, 500_000, f.feeCollector())
	requireInt(t, 245_000, f.credited(0))
}

// TestBeginBlockerNeverStopsTheChain checks the safety rule: if the payout
// fails part-way, the block still succeeds and the failure is recorded.
func TestBeginBlockerNeverStopsTheChain(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(1, 1)
	f.collect(coins(1_000_000))
	f.distr.failAllocate = true

	ctx := f.sdkCtx.WithBlockHeight(5).WithVoteInfos(votes)
	require.NoError(t, f.keeper.BeginBlocker(ctx), "a failed payout must not return an error")

	var recorded bool
	for _, event := range ctx.EventManager().Events() {
		if event.Type == keeper.EventTypeEqualShareFailed {
			recorded = true
		}
		require.NotEqual(t, keeper.EventTypeEqualShare, event.Type, "a failed payout must not be reported as done")
	}
	require.True(t, recorded, "the failure should be recorded as an event")
}

func TestParamsValidate(t *testing.T) {
	require.NoError(t, types.DefaultParams().Validate())
	require.Equal(t, uint64(5_000), types.DefaultParams().EqualShareBps)
	require.NoError(t, types.NewParams(0).Validate())
	require.NoError(t, types.NewParams(types.BasisPoints).Validate())
	require.ErrorIs(t, types.NewParams(types.BasisPoints+1).Validate(), types.ErrInvalidParams)
}
