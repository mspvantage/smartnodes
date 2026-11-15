package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

func (k msgServer) BuyTickets(ctx context.Context, msg *types.MsgBuyTickets) (*types.MsgBuyTicketsResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Buyer); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgBuyTicketsResponse{}, nil
}
