package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// ClaimNodeReward pays a node's operator the availability reward the node
// has built up. Only the operator may claim, and the pool pays exactly what
// the node's record says it is owed.
func (k msgServer) ClaimNodeReward(ctx context.Context, msg *types.MsgClaimNodeReward) (*types.MsgClaimNodeRewardResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}
	operatorStr, err := k.addressCodec.BytesToString(operator)
	if err != nil {
		return nil, err
	}

	node, err := k.registryKeeper.GetNode(ctx, msg.NodeId)
	if err != nil {
		return nil, err
	}
	if node.Operator != operatorStr {
		return nil, errorsmod.Wrapf(types.ErrNotOperator, "node %d", msg.NodeId)
	}

	rec, err := k.NodeRecord.Get(ctx, msg.NodeId)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrapf(types.ErrNothingToClaim, "node %d", msg.NodeId)
		}
		return nil, err
	}

	// Fix the share of the last round the node earned in, if that round has
	// been fully counted. If it has not, it stays pending for a later claim.
	if rec.PendingRound != 0 {
		done, err := k.roundTallied(ctx, rec.PendingRound)
		if err != nil {
			return nil, err
		}
		if done {
			if err := k.settleReward(ctx, &rec); err != nil {
				return nil, err
			}
		}
	}

	amount := rec.RewardBalance
	if amount == 0 {
		return nil, errorsmod.Wrapf(types.ErrNothingToClaim, "node %d", msg.NodeId)
	}

	owed, err := k.rewardOwed(ctx)
	if err != nil {
		return nil, err
	}
	if amount > owed {
		// The records disagree. Refuse to pay rather than pay coins the
		// pool has not set aside.
		return nil, errorsmod.Wrap(types.ErrInvalidAmount, "reward records are inconsistent")
	}

	// Update the records before paying out.
	rec.RewardBalance = 0
	rec.RewardPaid += amount
	if err := k.NodeRecord.Set(ctx, msg.NodeId, rec); err != nil {
		return nil, err
	}
	if err := k.RewardOwed.Set(ctx, owed-amount); err != nil {
		return nil, err
	}

	denom, err := k.registryKeeper.BondDenom(ctx)
	if err != nil {
		return nil, err
	}
	coins := sdk.NewCoins(sdk.NewCoin(denom, math.NewIntFromUint64(amount)))
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.RewardsPoolName, operator, coins); err != nil {
		return nil, err
	}

	emit(ctx, EventTypeClaimReward,
		sdk.NewAttribute(AttributeKeyNodeID, u64(msg.NodeId)),
		sdk.NewAttribute(AttributeKeyAmount, coins.String()),
	)

	return &types.MsgClaimNodeRewardResponse{Amount: amount}, nil
}
