package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/rewards/types"
)

// minSelf is the default minimum: 5,000 SNODE.
var minSelf = int64(types.DefaultEqualShareMinSelfStake)

// setSelfStake sets how much of validator i's stake is the operator's own.
func (f *fixture) setSelfStake(i int, amount int64) {
	f.staking.selfShares[operatorName(i)] = amount
}

func operatorName(i int) string { return "validator-" + string(rune('0'+i)) }

// A validator whose operator has too little of their own stake behind it
// gets no equal share. Its part goes to the validators that qualify.
func TestEqualShareNeedsSelfStake(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(10, 10, 10, 10)
	f.setSelfStake(2, minSelf-1) // just under
	f.setSelfStake(3, 50_000_000)
	f.collect(coins(1_000_000))

	require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, votes))

	// The equal part is 500,000, less 2% tax: 490,000 between the two that
	// qualify, not four.
	requireInt(t, 245_000, f.credited(0))
	requireInt(t, 245_000, f.credited(1))
	requireInt(t, 0, f.credited(2))
	requireInt(t, 0, f.credited(3))
	requireInt(t, 500_000, f.feeCollector())
	f.requireConserved(t, 1_000_000, 4)
}

func TestEqualShareAtExactlyTheMinimum(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(10, 10)
	f.setSelfStake(1, minSelf) // exactly the minimum qualifies
	f.collect(coins(1_000_000))

	require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, votes))
	requireInt(t, 245_000, f.credited(0))
	requireInt(t, 245_000, f.credited(1))
}

// If nobody qualifies, nothing is moved: every coin stays in the fee
// collector and is paid by stake in the normal way.
func TestEqualShareNobodyQualifies(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(10, 10, 10)
	for i := 0; i < 3; i++ {
		f.setSelfStake(i, 1)
	}
	f.collect(coins(1_000_000))

	require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, votes))
	for i := 0; i < 3; i++ {
		requireInt(t, 0, f.credited(i))
	}
	requireInt(t, 1_000_000, f.feeCollector())
	requireInt(t, 0, f.distribution())
	require.True(t, f.distr.communityPool.IsZero())
}

// Stake delegated by other people does not count. Only the operator's own
// delegation does, however large the validator is.
func TestOnlyTheOperatorsOwnStakeCounts(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(10, 10)
	// Validator 1 holds 10,000 SNODE in total, but only 100 of it is the
	// operator's own.
	f.setSelfStake(1, 100_000_000)
	f.collect(coins(1_000_000))

	require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, votes))
	requireInt(t, 490_000, f.credited(0))
	requireInt(t, 0, f.credited(1))
	f.requireConserved(t, 1_000_000, 2)
}

// An operator with no delegation to their own validator does not qualify.
func TestNoSelfDelegationDoesNotQualify(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(10, 10)
	f.setSelfStake(1, -1) // no delegation record at all
	f.collect(coins(1_000_000))

	require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, votes))
	requireInt(t, 490_000, f.credited(0))
	requireInt(t, 0, f.credited(1))
}

// A minimum of zero switches the rule off.
func TestZeroMinimumPaysEveryone(t *testing.T) {
	f := initFixture(t)
	params := types.DefaultParams()
	params.EqualShareMinSelfStake = 0
	require.NoError(t, params.Validate())
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))

	votes := f.validatorSet(10, 10)
	f.setSelfStake(1, -1)
	f.collect(coins(1_000_000))

	require.NoError(t, f.keeper.AllocateEqualShare(f.ctx, votes))
	requireInt(t, 245_000, f.credited(0))
	requireInt(t, 245_000, f.credited(1))
}

// Self stake is measured in tokens, so it falls when the validator is
// slashed: a slashed validator's shares are worth fewer tokens.
func TestSelfStakeIsMeasuredInTokens(t *testing.T) {
	f := initFixture(t)
	votes := f.validatorSet(10)
	cons := votes[0].Validator.Address
	v := f.staking.byCons[string(cons)]

	require.True(t, f.keeper.SelfStake(f.ctx, v).Equal(math.NewInt(fullSelfShares)))

	// Halve the tokens behind the same shares, as a 50% slash would.
	v.Tokens = math.NewInt(fullSelfShares / 2)
	f.staking.byCons[string(cons)] = v
	require.True(t, f.keeper.SelfStake(f.ctx, v).Equal(math.NewInt(fullSelfShares/2)))

	// A validator with no shares at all reads as zero, not as an error.
	v.DelegatorShares = math.LegacyZeroDec()
	require.True(t, f.keeper.SelfStake(f.ctx, v).IsZero())
}
