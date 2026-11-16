package keeper

import (
	"bytes"
	"context"

	errorsmod "cosmossdk.io/errors"

	"github.com/mspvantage/smartnodes/x/rewards/types"
)

func (k msgServer) UpdateParams(ctx context.Context, req *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	authority, err := k.addressCodec.StringToBytes(req.Authority)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}

	if !bytes.Equal(k.GetAuthority(), authority) {
		expectedAuthorityStr, _ := k.addressCodec.BytesToString(k.GetAuthority())
		return nil, errorsmod.Wrapf(types.ErrInvalidSigner, "invalid authority; expected %s, got %s", expectedAuthorityStr, req.Authority)
	}

	if err := req.Params.Validate(); err != nil {
		return nil, err
	}

	// The non-voting list is fixed at genesis. Governance may take addresses
	// off it, for example when a team member's vesting has ended, but may
	// never add one: otherwise a majority could silence anyone it chose.
	current, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if !types.NonVotingOnlyShrinks(current.NonVotingAddresses, req.Params.NonVotingAddresses) {
		return nil, types.ErrNonVotingListGrew
	}

	if err := k.Params.Set(ctx, req.Params); err != nil {
		return nil, err
	}

	return &types.MsgUpdateParamsResponse{}, nil
}
