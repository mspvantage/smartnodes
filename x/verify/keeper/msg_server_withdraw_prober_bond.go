package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

func (k msgServer) WithdrawProberBond(ctx context.Context, msg *types.MsgWithdrawProberBond) (*types.MsgWithdrawProberBondResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Owner); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgWithdrawProberBondResponse{}, nil
}
