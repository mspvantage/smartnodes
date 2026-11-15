package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

func (k msgServer) RevealReport(ctx context.Context, msg *types.MsgRevealReport) (*types.MsgRevealReportResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Owner); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgRevealReportResponse{}, nil
}
