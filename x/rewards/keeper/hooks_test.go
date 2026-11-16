package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/rewards/types"
)

// newValidator puts a validator with the given commission, in percent, in
// the mock staking module and returns its address.
func (f *fixture) newValidator(seed byte, ratePct, maxPct int64) sdk.ValAddress {
	addr := sdk.ValAddress([]byte{seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed})
	f.staking.byOper[string(addr)] = stakingtypes.Validator{
		Commission: stakingtypes.NewCommission(
			math.LegacyNewDecWithPrec(ratePct, 2),
			math.LegacyNewDecWithPrec(maxPct, 2),
			math.LegacyNewDecWithPrec(1, 2),
		),
	}
	return addr
}

// A new validator's maximum commission may not be above the ceiling. Because
// staking never lets commission go above that maximum, this caps commission
// for the life of the validator.
func TestCommissionCeiling(t *testing.T) {
	f := initFixture(t)
	hooks := f.keeper.Hooks()

	ceiling, err := f.keeper.MaxCommission(f.ctx)
	require.NoError(t, err)
	require.True(t, ceiling.Equal(math.LegacyNewDecWithPrec(20, 2)), "default ceiling is %s, want 0.20", ceiling)

	cases := []struct {
		name            string
		ratePct, maxPct int64
		allowed         bool
	}{
		{"5% now, 10% maximum", 5, 10, true},
		{"10% now, 20% maximum: exactly at the ceiling", 10, 20, true},
		{"20% now, 20% maximum", 20, 20, true},
		{"5% now, 21% maximum: low today, but could rise above the ceiling", 5, 21, false},
		{"10% now, 50% maximum", 10, 50, false},
		{"100% now, 100% maximum", 100, 100, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := f.newValidator(byte(i+1), tc.ratePct, tc.maxPct)
			err := hooks.AfterValidatorCreated(f.ctx, addr)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, types.ErrCommissionTooHigh)
			}
		})
	}
}

// Governance can move the ceiling; the new value applies to validators
// created afterwards.
func TestCommissionCeilingFollowsParams(t *testing.T) {
	f := initFixture(t)
	hooks := f.keeper.Hooks()
	addr := f.newValidator(1, 10, 30)
	require.ErrorIs(t, hooks.AfterValidatorCreated(f.ctx, addr), types.ErrCommissionTooHigh)

	params := types.DefaultParams()
	params.MaxCommissionBps = 3_000
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	require.NoError(t, hooks.AfterValidatorCreated(f.ctx, addr))

	// 10,000 means no ceiling.
	params.MaxCommissionBps = types.BasisPoints
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	require.NoError(t, hooks.AfterValidatorCreated(f.ctx, f.newValidator(2, 100, 100)))
}

// If a validator is created before this module's settings exist, the default
// ceiling applies instead of the check failing or being skipped.
func TestCommissionCeilingBeforeGenesis(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.Params.Remove(f.ctx))
	hooks := f.keeper.Hooks()
	require.NoError(t, hooks.AfterValidatorCreated(f.ctx, f.newValidator(1, 10, 20)))
	require.ErrorIs(t, hooks.AfterValidatorCreated(f.ctx, f.newValidator(2, 10, 21)), types.ErrCommissionTooHigh)
}

// A validator the staking module does not know cannot be approved.
func TestCommissionCeilingUnknownValidator(t *testing.T) {
	f := initFixture(t)
	err := f.keeper.Hooks().AfterValidatorCreated(f.ctx, sdk.ValAddress("nobody"))
	require.ErrorIs(t, err, stakingtypes.ErrNoValidatorFound)
}

// The hooks this module does not use must never block staking.
func TestUnusedHooksAllowEverything(t *testing.T) {
	f := initFixture(t)
	h := f.keeper.Hooks()
	val := sdk.ValAddress("v")
	cons := sdk.ConsAddress("c")
	del := sdk.AccAddress("d")
	require.NoError(t, h.BeforeValidatorModified(f.ctx, val))
	require.NoError(t, h.AfterValidatorRemoved(f.ctx, cons, val))
	require.NoError(t, h.AfterValidatorConsKeyUpdated(f.ctx, cons, cons, val))
	require.NoError(t, h.AfterValidatorBonded(f.ctx, cons, val))
	require.NoError(t, h.AfterValidatorBeginUnbonding(f.ctx, cons, val))
	require.NoError(t, h.BeforeDelegationCreated(f.ctx, del, val))
	require.NoError(t, h.BeforeDelegationSharesModified(f.ctx, del, val))
	require.NoError(t, h.BeforeDelegationRemoved(f.ctx, del, val))
	require.NoError(t, h.AfterDelegationModified(f.ctx, del, val))
	require.NoError(t, h.BeforeValidatorSlashed(f.ctx, val, math.LegacyOneDec()))
	require.NoError(t, h.AfterUnbondingInitiated(f.ctx, 1))
}

func TestMaxCommissionParamValidation(t *testing.T) {
	p := types.DefaultParams()
	require.Equal(t, uint64(2_000), p.MaxCommissionBps)
	require.NoError(t, p.Validate())

	p.MaxCommissionBps = 1
	require.NoError(t, p.Validate())
	p.MaxCommissionBps = types.BasisPoints
	require.NoError(t, p.Validate())

	p.MaxCommissionBps = 0
	require.ErrorIs(t, p.Validate(), types.ErrInvalidParams)
	p.MaxCommissionBps = types.BasisPoints + 1
	require.ErrorIs(t, p.Validate(), types.ErrInvalidParams)
}
