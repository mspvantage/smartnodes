package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/mspvantage/smartnodes/x/rewards/types"
)

// Hooks lets the rewards module react to staking events. It uses one: when a
// validator is created, its maximum commission is checked against the
// ceiling.
//
// This one check caps commission for the life of the validator. Staking never
// lets a validator raise its commission above the maximum it chose at
// creation, and that maximum cannot be changed afterwards.
type Hooks struct {
	k Keeper
}

var _ stakingtypes.StakingHooks = Hooks{}

// Hooks returns the rewards module's staking hooks.
func (k Keeper) Hooks() Hooks {
	return Hooks{k}
}

// MaxCommission returns the commission ceiling as a decimal: 2,000 basis
// points gives 0.20.
func (k Keeper) MaxCommission(ctx context.Context) (math.LegacyDec, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return math.LegacyDec{}, err
		}
		// Not set yet. This only happens if a validator is created before
		// this module's genesis has run; fall back to the default.
		params = types.DefaultParams()
	}
	return math.LegacyNewDecWithPrec(int64(params.MaxCommissionBps), 4), nil
}

// AfterValidatorCreated refuses a new validator whose maximum commission is
// above the ceiling. Returning an error fails the transaction that tried to
// create it, so the validator never exists.
func (h Hooks) AfterValidatorCreated(ctx context.Context, valAddr sdk.ValAddress) error {
	ceiling, err := h.k.MaxCommission(ctx)
	if err != nil {
		return err
	}
	val, err := h.k.stakingKeeper.GetValidator(ctx, valAddr)
	if err != nil {
		return err
	}
	if val.Commission.MaxRate.GT(ceiling) {
		return errorsmod.Wrapf(types.ErrCommissionTooHigh, "maximum commission %s, ceiling %s", val.Commission.MaxRate, ceiling)
	}
	return nil
}

// The remaining hooks are not used.

func (h Hooks) BeforeValidatorModified(context.Context, sdk.ValAddress) error { return nil }

func (h Hooks) AfterValidatorRemoved(context.Context, sdk.ConsAddress, sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterValidatorConsKeyUpdated(context.Context, sdk.ConsAddress, sdk.ConsAddress, sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterValidatorBonded(context.Context, sdk.ConsAddress, sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterValidatorBeginUnbonding(context.Context, sdk.ConsAddress, sdk.ValAddress) error {
	return nil
}

func (h Hooks) BeforeDelegationCreated(context.Context, sdk.AccAddress, sdk.ValAddress) error {
	return nil
}

func (h Hooks) BeforeDelegationSharesModified(context.Context, sdk.AccAddress, sdk.ValAddress) error {
	return nil
}

func (h Hooks) BeforeDelegationRemoved(context.Context, sdk.AccAddress, sdk.ValAddress) error {
	return nil
}

func (h Hooks) AfterDelegationModified(context.Context, sdk.AccAddress, sdk.ValAddress) error {
	return nil
}

func (h Hooks) BeforeValidatorSlashed(context.Context, sdk.ValAddress, math.LegacyDec) error {
	return nil
}

func (h Hooks) AfterUnbondingInitiated(context.Context, uint64) error { return nil }
