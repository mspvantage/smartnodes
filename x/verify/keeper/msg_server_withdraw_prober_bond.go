package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// WithdrawProberBond returns the bond once the prober has left the active set
// and its unbonding period has ended, and removes the prober. The address is
// then free to register again.
func (k msgServer) WithdrawProberBond(ctx context.Context, msg *types.MsgWithdrawProberBond) (*types.MsgWithdrawProberBondResponse, error) {
	owner, err := k.addressCodec.StringToBytes(msg.Owner)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid owner address")
	}
	ownerStr, err := k.addressCodec.BytesToString(owner)
	if err != nil {
		return nil, err
	}

	prober, err := k.proberByOwner(ctx, ownerStr)
	if err != nil {
		return nil, err
	}
	// A leaving prober is still in the active set until the next round
	// starts, so its bond cannot be released yet.
	if prober.Status != types.ProberUnbonding {
		return nil, errorsmod.Wrapf(types.ErrProberNotUnbonding, "prober %d", prober.Id)
	}
	if now := blockUnix(ctx); now < prober.UnbondingEnd {
		return nil, errorsmod.Wrapf(types.ErrUnbondingNotOver, "prober %d: %d seconds left", prober.Id, prober.UnbondingEnd-now)
	}

	// Remove the records before paying out.
	if err := k.Prober.Remove(ctx, prober.Id); err != nil {
		return nil, err
	}
	if err := k.ProberByOwner.Remove(ctx, ownerStr); err != nil {
		return nil, err
	}

	bond, err := k.bondCoins(ctx, prober.Bond)
	if err != nil {
		return nil, err
	}
	if prober.Bond > 0 {
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, owner, bond); err != nil {
			return nil, err
		}
	}

	emit(ctx, EventTypeWithdrawProber,
		sdk.NewAttribute(AttributeKeyProberID, u64(prober.Id)),
		sdk.NewAttribute(AttributeKeyAmount, bond.String()),
	)

	return &types.MsgWithdrawProberBondResponse{}, nil
}
