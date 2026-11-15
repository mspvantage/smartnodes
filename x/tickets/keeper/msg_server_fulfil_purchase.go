package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

func (k msgServer) FulfilPurchase(ctx context.Context, msg *types.MsgFulfilPurchase) (*types.MsgFulfilPurchaseResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Sender); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgFulfilPurchaseResponse{}, nil
}
