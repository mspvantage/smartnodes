package keeper

import (
	"context"
	"strconv"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// SetReality publishes or withdraws an entry node's disguised way in. Only
// the operator may send it, and only for an active entry node: exit nodes
// are reached inside the entry tunnel, where no censor can see them.
func (k msgServer) SetReality(ctx context.Context, msg *types.MsgSetReality) (*types.MsgSetRealityResponse, error) {
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
	if node.Commitment != "" {
		return nil, errorsmod.Wrap(types.ErrIsBridge, "a bridge's disguise goes in its bridge line, never on the chain")
	}
	if node.Tier != types.TierEntry {
		return nil, errorsmod.Wrap(types.ErrInvalidReality, "only entry nodes offer a disguise")
	}

	var next *types.Reality
	if msg.Port != 0 {
		r := types.Reality{Port: msg.Port, PublicKey: msg.PublicKey, ShortId: msg.ShortId, ServerName: msg.ServerName, Uuid: msg.Uuid}
		if err := types.ValidateReality(r); err != nil {
			return nil, err
		}
		next = &r
	} else if msg.PublicKey != "" || msg.ShortId != "" || msg.ServerName != "" || msg.Uuid != "" {
		return nil, errorsmod.Wrap(types.ErrInvalidReality, "to remove the disguise, send port 0 with every other value empty")
	}
	if (next == nil && node.Reality == nil) || (next != nil && node.Reality != nil && *next == *node.Reality) {
		return nil, types.ErrNothingToUpdate
	}

	node.Reality = next
	if err := k.Node.Set(ctx, node.Id, node); err != nil {
		return nil, err
	}
	k.emit(ctx, EventTypeSetReality, node.Id, sdk.NewAttribute(AttributeKeyRealityPort, strconv.FormatUint(uint64(msg.Port), 10)))
	return &types.MsgSetRealityResponse{}, nil
}
