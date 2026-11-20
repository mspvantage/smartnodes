package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

// Unjail returns a jailed node to service. Only the node's operator may do
// it, and only after the waiting period. The count of failed rounds starts
// again from zero; the node becomes verified again when it next passes.
func (k msgServer) Unjail(ctx context.Context, msg *types.MsgUnjail) (*types.MsgUnjailResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}
	operatorStr, err := k.addressCodec.BytesToString(operator)
	if err != nil {
		return nil, err
	}

	node, err := k.registryKeeper.GetNode(ctx, msg.NodeId)
	if err != nil {
		return nil, err
	}
	if node.Operator != operatorStr {
		return nil, errorsmod.Wrapf(types.ErrNotOperator, "node %d", msg.NodeId)
	}
	if node.Status != registrytypes.StatusActive {
		return nil, errorsmod.Wrapf(types.ErrNodeNotActive, "node %d", msg.NodeId)
	}

	rec, err := k.NodeRecord.Get(ctx, msg.NodeId)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrapf(types.ErrNodeNotJailed, "node %d", msg.NodeId)
		}
		return nil, err
	}
	if !rec.Jailed {
		return nil, errorsmod.Wrapf(types.ErrNodeNotJailed, "node %d", msg.NodeId)
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	free := rec.JailedAt + int64(params.UnjailWaitSeconds)
	if now := blockUnix(ctx); now < free {
		return nil, errorsmod.Wrapf(types.ErrJailNotOver, "node %d: %d seconds left", msg.NodeId, free-now)
	}

	rec.Jailed = false
	rec.ConsecutiveFails = 0
	if round, _, err := k.CurrentRound(ctx); err == nil {
		// From the next round, so this round's assignments stay as they were.
		rec.UnjailedRound = round + 1
	}
	if err := k.NodeRecord.Set(ctx, msg.NodeId, rec); err != nil {
		return nil, err
	}

	emit(ctx, EventTypeUnjail, sdk.NewAttribute(AttributeKeyNodeID, u64(msg.NodeId)))

	return &types.MsgUnjailResponse{}, nil
}
