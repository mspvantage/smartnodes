package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/registry/types"
)

func (k msgServer) AddBond(ctx context.Context, msg *types.MsgAddBond) (*types.MsgAddBondResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Operator); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgAddBondResponse{}, nil
}
