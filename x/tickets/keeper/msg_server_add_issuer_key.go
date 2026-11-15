package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

func (k msgServer) AddIssuerKey(ctx context.Context, msg *types.MsgAddIssuerKey) (*types.MsgAddIssuerKeyResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Authority); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgAddIssuerKeyResponse{}, nil
}
