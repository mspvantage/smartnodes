package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// AddBond tops up the bond of an active node, for example after a slash.
func (k msgServer) AddBond(ctx context.Context, msg *types.MsgAddBond) (*types.MsgAddBondResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}

	node, err := k.ownedNode(ctx, msg.Id, operator)
	if err != nil {
		return nil, err
	}
	if node.Status != types.StatusActive {
		return nil, errorsmod.Wrapf(types.ErrNodeNotActive, "node %d", msg.Id)
	}
	if err := k.checkBondCoin(ctx, msg.Amount); err != nil {
		return nil, err
	}
	if msg.Amount.Denom != node.Bond.Denom {
		return nil, errorsmod.Wrapf(types.ErrInvalidBond, "denom %s, node is bonded in %s", msg.Amount.Denom, node.Bond.Denom)
	}

	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, operator, types.ModuleName, sdk.NewCoins(msg.Amount)); err != nil {
		return nil, err
	}

	node.Bond = node.Bond.Add(msg.Amount)
	if err := k.Node.Set(ctx, node.Id, node); err != nil {
		return nil, err
	}

	k.emit(ctx, EventTypeAddBond, node.Id, sdk.NewAttribute(AttributeKeyAmount, msg.Amount.String()))

	return &types.MsgAddBondResponse{}, nil
}
