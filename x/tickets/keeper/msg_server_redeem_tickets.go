package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

func (k msgServer) RedeemTickets(ctx context.Context, msg *types.MsgRedeemTickets) (*types.MsgRedeemTicketsResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Operator); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgRedeemTicketsResponse{}, nil
}
