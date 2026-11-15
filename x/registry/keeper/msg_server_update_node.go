package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// UpdateNode changes an active node's endpoint, public key, or both. An empty
// field means "leave unchanged". The tier cannot be changed: deregister and
// register again instead, so the right minimum bond is enforced.
func (k msgServer) UpdateNode(ctx context.Context, msg *types.MsgUpdateNode) (*types.MsgUpdateNodeResponse, error) {
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

	changed := false

	if msg.Endpoint != "" && msg.Endpoint != node.Endpoint {
		if err := types.ValidateEndpoint(msg.Endpoint); err != nil {
			return nil, err
		}
		node.Endpoint = msg.Endpoint
		changed = true
	}

	if msg.Pubkey != "" && msg.Pubkey != node.Pubkey {
		if err := types.ValidatePubkey(msg.Pubkey); err != nil {
			return nil, err
		}
		taken, err := k.NodeByPubkey.Has(ctx, msg.Pubkey)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, types.ErrPubkeyTaken
		}
		if err := k.NodeByPubkey.Remove(ctx, node.Pubkey); err != nil {
			return nil, err
		}
		if err := k.NodeByPubkey.Set(ctx, msg.Pubkey, node.Id); err != nil {
			return nil, err
		}
		node.Pubkey = msg.Pubkey
		changed = true
	}

	if !changed {
		return nil, types.ErrNothingToUpdate
	}

	if err := k.Node.Set(ctx, node.Id, node); err != nil {
		return nil, err
	}

	k.emit(ctx, EventTypeUpdateNode, node.Id)

	return &types.MsgUpdateNodeResponse{}, nil
}
