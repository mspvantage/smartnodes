package keeper

import (
	"context"
	"strconv"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// DeregisterProber starts a prober's exit. The unbonding period is fixed at
// this moment: a later parameter change does not move it.
//
// An active prober stays in the active set, and can still be assigned nodes,
// until the next round starts, because the set must not change mid-round. A
// prober that has not joined yet goes straight to unbonding.
func (k msgServer) DeregisterProber(ctx context.Context, msg *types.MsgDeregisterProber) (*types.MsgDeregisterProberResponse, error) {
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
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	switch prober.Status {
	case types.ProberPending:
		if err := k.PendingJoin.Remove(ctx, prober.Id); err != nil {
			return nil, err
		}
		prober.Status = types.ProberUnbonding
	case types.ProberActive:
		if err := k.PendingLeave.Set(ctx, prober.Id); err != nil {
			return nil, err
		}
		prober.Status = types.ProberLeaving
	default:
		return nil, errorsmod.Wrapf(types.ErrProberLeaving, "prober %d", prober.Id)
	}

	prober.UnbondingEnd = blockUnix(ctx) + int64(params.UnbondingSeconds)
	if err := k.Prober.Set(ctx, prober.Id, prober); err != nil {
		return nil, err
	}

	emit(ctx, EventTypeDeregisterProber,
		sdk.NewAttribute(AttributeKeyProberID, u64(prober.Id)),
		sdk.NewAttribute(AttributeKeyUnbondingEnd, strconv.FormatInt(prober.UnbondingEnd, 10)),
	)

	return &types.MsgDeregisterProberResponse{}, nil
}
