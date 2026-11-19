package keeper

import (
	"bytes"
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// SetServiceKey sets or clears the account that may redeem a node's tickets.
//
// A node must redeem tickets around the clock, so it needs a signing key on
// an internet-facing server. Without a service key that key is the
// operator's own, which can also deregister the node and withdraw its bond.
// A service key can only redeem: if the server is broken into, the attacker
// gets the right to collect payments for the operator, nothing more. Only the
// operator may set it; a sponsor may not.
func (k msgServer) SetServiceKey(ctx context.Context, msg *types.MsgSetServiceKey) (*types.MsgSetServiceKeyResponse, error) {
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

	if msg.ServiceKey != "" {
		key, err := k.addressCodec.StringToBytes(msg.ServiceKey)
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidServiceKey, "not a valid address")
		}
		if bytes.Equal(key, operator) {
			return nil, errorsmod.Wrap(types.ErrInvalidServiceKey, "the operator can already redeem; set a different account")
		}
		// Store the canonical form, so comparisons are exact.
		if msg.ServiceKey, err = k.addressCodec.BytesToString(key); err != nil {
			return nil, err
		}
	}
	if msg.ServiceKey == node.ServiceKey {
		return nil, types.ErrNothingToUpdate
	}

	node.ServiceKey = msg.ServiceKey
	if err := k.Node.Set(ctx, node.Id, node); err != nil {
		return nil, err
	}
	k.emit(ctx, EventTypeSetServiceKey, node.Id, sdk.NewAttribute(AttributeKeyServiceKey, msg.ServiceKey))
	return &types.MsgSetServiceKeyResponse{}, nil
}
