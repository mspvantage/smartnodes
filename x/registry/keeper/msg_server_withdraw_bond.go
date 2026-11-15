package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// WithdrawBond returns what is left of the bond once unbonding has ended and
// removes the node. The public key becomes free to register again.
func (k msgServer) WithdrawBond(ctx context.Context, msg *types.MsgWithdrawBond) (*types.MsgWithdrawBondResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}

	node, err := k.ownedNode(ctx, msg.Id, operator)
	if err != nil {
		return nil, err
	}
	if node.Status != types.StatusUnbonding {
		return nil, errorsmod.Wrapf(types.ErrNodeNotUnbonding, "node %d", msg.Id)
	}
	if now := k.blockUnix(ctx); now < node.UnbondingEnd {
		return nil, errorsmod.Wrapf(types.ErrUnbondingNotOver, "node %d: %d seconds left", msg.Id, node.UnbondingEnd-now)
	}

	// Remove the records before paying out.
	if err := k.Node.Remove(ctx, node.Id); err != nil {
		return nil, err
	}
	if err := k.NodeByPubkey.Remove(ctx, node.Pubkey); err != nil {
		return nil, err
	}

	// The bond can be zero if the node was fully slashed.
	if node.Bond.IsPositive() {
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, operator, sdk.NewCoins(node.Bond)); err != nil {
			return nil, err
		}
	}

	k.emit(ctx, EventTypeWithdrawBond, node.Id, sdk.NewAttribute(AttributeKeyAmount, node.Bond.String()))

	return &types.MsgWithdrawBondResponse{}, nil
}
