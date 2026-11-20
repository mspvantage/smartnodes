package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// WithdrawBond returns what is left of the bond once unbonding has ended and
// removes the node. The public key becomes free to register again. For a
// sponsored node, the sponsored part returns to the sponsor pool.
func (k msgServer) WithdrawBond(ctx context.Context, msg *types.MsgWithdrawBond) (*types.MsgWithdrawBondResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}

	signer := operator
	node, err := k.controlledNode(ctx, msg.Id, signer)
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
	if node.Pubkey != "" { // a bridge has none
		if err := k.NodeByPubkey.Remove(ctx, node.Pubkey); err != nil {
			return nil, err
		}
	}

	// What is left of the sponsored part goes back to the sponsor pool. The
	// rest belongs to the node's operator, whoever sent this message. The
	// bond can be zero if the node was fully slashed.
	owner, err := k.addressCodec.StringToBytes(node.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidNode, "stored operator address is invalid")
	}
	toPool := math.MinInt(math.NewIntFromUint64(node.Sponsored), node.Bond.Amount)
	toOperator := node.Bond.Amount.Sub(toPool)
	if toPool.IsPositive() {
		coins := sdk.NewCoins(sdk.NewCoin(node.Bond.Denom, toPool))
		if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.ModuleName, types.SponsorPoolName, coins); err != nil {
			return nil, err
		}
	}
	if node.Sponsored > 0 {
		if err := k.addSponsoredCount(ctx, -1); err != nil {
			return nil, err
		}
	}
	if toOperator.IsPositive() {
		coins := sdk.NewCoins(sdk.NewCoin(node.Bond.Denom, toOperator))
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, owner, coins); err != nil {
			return nil, err
		}
	}

	k.emit(ctx, EventTypeWithdrawBond, node.Id,
		sdk.NewAttribute(AttributeKeyAmount, node.Bond.String()),
		sdk.NewAttribute(AttributeKeyReturnedToPool, toPool.String()),
	)

	return &types.MsgWithdrawBondResponse{}, nil
}
