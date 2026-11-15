package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/registry/types"
)

func (k msgServer) UpdateNode(ctx context.Context, msg *types.MsgUpdateNode) (*types.MsgUpdateNodeResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Operator); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgUpdateNodeResponse{}, nil
}
