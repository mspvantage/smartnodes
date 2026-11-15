package keeper

import (
	"context"
	"strconv"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// DeregisterNode takes an active node out of service and starts its unbonding
// period. The bond stays locked, and slashable, until the period ends. The
// period is fixed at this moment: a later parameter change does not move it.
func (k msgServer) DeregisterNode(ctx context.Context, msg *types.MsgDeregisterNode) (*types.MsgDeregisterNodeResponse, error) {
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

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	node.Status = types.StatusUnbonding
	node.UnbondingEnd = k.blockUnix(ctx) + int64(params.UnbondingSeconds)
	if err := k.Node.Set(ctx, node.Id, node); err != nil {
		return nil, err
	}

	k.emit(ctx, EventTypeDeregisterNode, node.Id,
		sdk.NewAttribute(AttributeKeyUnbondingEnd, strconv.FormatInt(node.UnbondingEnd, 10)),
	)

	return &types.MsgDeregisterNodeResponse{}, nil
}
