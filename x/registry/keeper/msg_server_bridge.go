package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// RegisterBridge registers an unlisted entry node. Like any node it posts
// at least the entry bond and earns from the tickets it redeems. Unlike a
// listed node, nothing about where it is or how to reach it goes on the
// chain: only the fingerprint of the bridge line its operator hands out.
func (k msgServer) RegisterBridge(ctx context.Context, msg *types.MsgRegisterBridge) (*types.MsgRegisterBridgeResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}
	if err := types.ValidateCommitment(msg.Commitment); err != nil {
		return nil, err
	}
	if err := k.checkBondCoin(ctx, msg.Bond); err != nil {
		return nil, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	minBond, err := params.MinBond(types.TierEntry)
	if err != nil {
		return nil, err
	}
	if msg.Bond.Amount.LT(math.NewIntFromUint64(minBond)) {
		return nil, errorsmod.Wrapf(types.ErrBondTooLow, "got %s, minimum %d%s", msg.Bond, minBond, msg.Bond.Denom)
	}

	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, operator, types.ModuleName, sdk.NewCoins(msg.Bond)); err != nil {
		return nil, err
	}
	id, err := k.NodeSeq.Next(ctx)
	if err != nil {
		return nil, err
	}
	operatorStr, err := k.addressCodec.BytesToString(operator)
	if err != nil {
		return nil, err
	}
	node := types.Node{
		Id:         id,
		Operator:   operatorStr,
		Tier:       types.TierEntry,
		Bond:       msg.Bond,
		Status:     types.StatusActive,
		Commitment: msg.Commitment,
	}
	if err := k.Node.Set(ctx, id, node); err != nil {
		return nil, err
	}
	k.emit(ctx, EventTypeRegisterBridge, id,
		sdk.NewAttribute(AttributeKeyOperator, operatorStr),
		sdk.NewAttribute(AttributeKeyAmount, msg.Bond.String()),
	)
	return &types.MsgRegisterBridgeResponse{Id: id}, nil
}

// UpdateBridge replaces a bridge's fingerprint. A bridge that a censor has
// blocked moves to a new address; its operator hands out the new line.
func (k msgServer) UpdateBridge(ctx context.Context, msg *types.MsgUpdateBridge) (*types.MsgUpdateBridgeResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}
	if err := types.ValidateCommitment(msg.Commitment); err != nil {
		return nil, err
	}
	node, err := k.ownedNode(ctx, msg.Id, operator)
	if err != nil {
		return nil, err
	}
	if node.Status != types.StatusActive {
		return nil, errorsmod.Wrapf(types.ErrNodeNotActive, "node %d", msg.Id)
	}
	if node.Commitment == "" {
		return nil, errorsmod.Wrap(types.ErrIsBridge, "node is listed, not a bridge")
	}
	if node.Commitment == msg.Commitment {
		return nil, types.ErrNothingToUpdate
	}
	node.Commitment = msg.Commitment
	if err := k.Node.Set(ctx, node.Id, node); err != nil {
		return nil, err
	}
	k.emit(ctx, EventTypeUpdateBridge, node.Id)
	return &types.MsgUpdateBridgeResponse{}, nil
}
