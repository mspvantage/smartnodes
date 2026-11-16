package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// FundNodeRewards adds the sender's coins to the node rewards pool. Anyone
// may fund the pool; nobody can take coins out except node operators
// claiming what their nodes earned.
func (k msgServer) FundNodeRewards(ctx context.Context, msg *types.MsgFundNodeRewards) (*types.MsgFundNodeRewardsResponse, error) {
	sender, err := k.addressCodec.StringToBytes(msg.Sender)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid sender address")
	}
	if msg.Amount == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidAmount, "amount must be greater than zero")
	}

	denom, err := k.registryKeeper.BondDenom(ctx)
	if err != nil {
		return nil, err
	}
	coins := sdk.NewCoins(sdk.NewCoin(denom, math.NewIntFromUint64(msg.Amount)))
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, sender, types.RewardsPoolName, coins); err != nil {
		return nil, err
	}

	emit(ctx, EventTypeFundRewards,
		sdk.NewAttribute(AttributeKeyOwner, msg.Sender),
		sdk.NewAttribute(AttributeKeyAmount, coins.String()),
	)

	return &types.MsgFundNodeRewardsResponse{}, nil
}
