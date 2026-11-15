package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/registry/types"
)

func (k msgServer) DeregisterNode(ctx context.Context, msg *types.MsgDeregisterNode) (*types.MsgDeregisterNodeResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Operator); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgDeregisterNodeResponse{}, nil
}
