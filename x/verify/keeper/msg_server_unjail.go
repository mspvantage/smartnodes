package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

func (k msgServer) Unjail(ctx context.Context, msg *types.MsgUnjail) (*types.MsgUnjailResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Operator); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgUnjailResponse{}, nil
}
