package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// RegisterProber locks a bond and queues the sender to join the active
// prober set when the next round starts. One address may run one prober.
func (k msgServer) RegisterProber(ctx context.Context, msg *types.MsgRegisterProber) (*types.MsgRegisterProberResponse, error) {
	owner, err := k.addressCodec.StringToBytes(msg.Owner)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid owner address")
	}
	// Store the canonical text form of the address, whatever case was sent.
	ownerStr, err := k.addressCodec.BytesToString(owner)
	if err != nil {
		return nil, err
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Bond < params.MinProberBond {
		return nil, errorsmod.Wrapf(types.ErrBondTooLow, "got %d, minimum %d", msg.Bond, params.MinProberBond)
	}

	taken, err := k.ProberByOwner.Has(ctx, ownerStr)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, types.ErrAlreadyProber
	}

	// Lock the bond first. If the owner cannot pay, nothing is stored.
	bond, err := k.bondCoins(ctx, msg.Bond)
	if err != nil {
		return nil, err
	}
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, owner, types.ModuleName, bond); err != nil {
		return nil, err
	}

	id, err := k.ProberSeq.Next(ctx)
	if err != nil {
		return nil, err
	}
	prober := types.Prober{
		Id:     id,
		Owner:  ownerStr,
		Bond:   msg.Bond,
		Status: types.ProberPending,
	}
	if err := k.Prober.Set(ctx, id, prober); err != nil {
		return nil, err
	}
	if err := k.ProberByOwner.Set(ctx, ownerStr, id); err != nil {
		return nil, err
	}
	if err := k.PendingJoin.Set(ctx, id); err != nil {
		return nil, err
	}

	emit(ctx, EventTypeRegisterProber,
		sdk.NewAttribute(AttributeKeyProberID, u64(id)),
		sdk.NewAttribute(AttributeKeyOwner, ownerStr),
		sdk.NewAttribute(AttributeKeyAmount, bond.String()),
	)

	return &types.MsgRegisterProberResponse{Id: id}, nil
}
