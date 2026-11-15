package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

func (k msgServer) CommitReports(ctx context.Context, msg *types.MsgCommitReports) (*types.MsgCommitReportsResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Owner); err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	// TODO: Handle the message

	return &types.MsgCommitReportsResponse{}, nil
}
