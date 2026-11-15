package keeper

import (
	"context"
	"strconv"

	abci "github.com/cometbft/cometbft/abci/types"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/mspvantage/smartnodes/x/rewards/types"
)

// Event types and attribute keys emitted by the rewards module.
const (
	EventTypeEqualShare       = "rewards_equal_share"
	EventTypeEqualShareFailed = "rewards_equal_share_failed"

	AttributeKeyValidators   = "validators"
	AttributeKeyPerValidator = "per_validator"
	AttributeKeyCommunity    = "community_pool"
	AttributeKeyReason       = "reason"
)

// BeginBlocker pays the equal share for the previous block.
//
// The standard distribution module pays validators purely in proportion to
// their stake, which lets the largest keep growing. This module takes a set
// fraction of what is in the fee collector (transaction fees, the stakers'
// share of ticket fees, and newly issued coins) and pays it in equal parts to
// every validator in the last block's validator set. The distribution module
// then pays whatever is left by stake.
//
// The work runs on a copy of the state. If anything fails, the copy is
// discarded, an event records why, and the block carries on: everything is
// then paid by stake, as on a standard chain. A fault here must never stop
// the chain.
func (k Keeper) BeginBlocker(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// Block 1 has no previous block to pay for.
	if sdkCtx.BlockHeight() <= 1 {
		return nil
	}

	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.AllocateEqualShare(cacheCtx, sdkCtx.VoteInfos()); err != nil {
		sdkCtx.Logger().Error("rewards: equal share skipped for this block", "error", err.Error())
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(EventTypeEqualShareFailed,
			sdk.NewAttribute(AttributeKeyReason, err.Error()),
		))
		return nil
	}
	write()
	return nil
}

// AllocateEqualShare moves the equal share out of the fee collector and
// credits it to the validators in votes, the same amount each.
//
// All arithmetic is in whole coins, so nothing is created or lost:
//
//	equal part   = collected * equal_share_bps / 10,000   (rounded down)
//	community    = equal part * community tax             (rounded down)
//	each         = (equal part - community) / validators  (rounded down)
//	leftover     = what the two roundings above did not hand out
//
// "each" goes to every validator, and community plus leftover goes to the
// community pool. Whatever was not part of the equal part stays in the fee
// collector for the distribution module.
func (k Keeper) AllocateEqualShare(ctx context.Context, votes []abci.VoteInfo) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if params.EqualShareBps == 0 {
		return nil
	}

	feeCollector := authtypes.NewModuleAddress(authtypes.FeeCollectorName)
	collected := k.bankKeeper.GetAllBalances(ctx, feeCollector)
	if collected.IsZero() {
		return nil
	}

	// The validators of the last block. One that has since left the set is
	// skipped; its part is shared among the rest.
	validators := make([]stakingtypes.ValidatorI, 0, len(votes))
	for _, vote := range votes {
		validator, err := k.stakingKeeper.ValidatorByConsAddr(ctx, vote.Validator.Address)
		if err != nil || validator == nil {
			continue
		}
		validators = append(validators, validator)
	}
	count := int64(len(validators))
	if count == 0 {
		return nil
	}

	communityTax, err := k.distrKeeper.GetCommunityTax(ctx)
	if err != nil {
		return err
	}

	bps := sdkmath.NewIntFromUint64(params.EqualShareBps)
	denominator := sdkmath.NewIntFromUint64(types.BasisPoints)

	perValidator := sdk.NewCoins()
	toValidators := sdk.NewCoins()
	toCommunity := sdk.NewCoins()

	for _, coin := range collected {
		equalPart := coin.Amount.Mul(bps).Quo(denominator)
		community := communityTax.MulInt(equalPart).TruncateInt()
		each := equalPart.Sub(community).QuoRaw(count)
		// Too little to give every validator even one unit: leave this denom
		// alone, so it is paid by stake instead of drifting to the community pool.
		if !each.IsPositive() {
			continue
		}
		handedOut := each.MulRaw(count)
		leftover := equalPart.Sub(handedOut)

		perValidator = perValidator.Add(sdk.NewCoin(coin.Denom, each))
		toValidators = toValidators.Add(sdk.NewCoin(coin.Denom, handedOut))
		if leftover.IsPositive() {
			toCommunity = toCommunity.Add(sdk.NewCoin(coin.Denom, leftover))
		}
	}
	if toValidators.IsZero() {
		return nil
	}

	if !toCommunity.IsZero() {
		if err := k.distrKeeper.FundCommunityPool(ctx, toCommunity, feeCollector); err != nil {
			return err
		}
	}

	// The distribution module holds the coins validators and delegators later
	// withdraw; crediting a validator there is bookkeeping against that balance.
	if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, authtypes.FeeCollectorName, distrtypes.ModuleName, toValidators); err != nil {
		return err
	}
	reward := sdk.NewDecCoinsFromCoins(perValidator...)
	for _, validator := range validators {
		if err := k.distrKeeper.AllocateTokensToValidator(ctx, validator, reward); err != nil {
			return err
		}
	}

	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(EventTypeEqualShare,
		sdk.NewAttribute(AttributeKeyValidators, strconv.FormatInt(count, 10)),
		sdk.NewAttribute(AttributeKeyPerValidator, perValidator.String()),
		sdk.NewAttribute(AttributeKeyCommunity, toCommunity.String()),
	))

	return nil
}
