package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// FlagNode records a customer's flag on a node.
//
// The flag spends a ticket made out to the node, so only someone who holds
// such a ticket can flag, each ticket flags once, and the ticket pays the
// node as usual: flagging a rival costs a ticket each time and earns the
// rival money. The ticket is blind-signed, so it does not show who bought
// it. A flag is not a verdict. It only makes the node probed more often,
// from the next round for aim_window_rounds rounds; how often a node is
// probed does not change what it earns over time, so flags cannot hurt an
// honest node. Probers, not flags, decide whether it passes.
//
// The design's stronger form proves that one of the node's redeemed tickets
// was the flagger's, without spending another; that waits on the
// cryptographer's review, like proof of probe.
func (k msgServer) FlagNode(ctx context.Context, msg *types.MsgFlagNode) (*types.MsgFlagNodeResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Signer); err != nil {
		return nil, errorsmod.Wrap(err, "invalid signer address")
	}
	if err := k.ticketsKeeper.RedeemForFlag(ctx, msg.NodeId, msg.KeyId, msg.Ticket); err != nil {
		return nil, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	round, _, err := k.CurrentRound(ctx)
	if err != nil {
		return nil, err
	}
	rec, err := k.NodeRecord.Get(ctx, msg.NodeId)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return nil, err
		}
		rec = types.NodeRecord{NodeId: msg.NodeId}
	}
	// From the next round, so this round's assignments stay as they were.
	// Flags extend the window; they do not stack the rate.
	if rec.FlaggedUntil < round+1 {
		rec.FlaggedFrom = round + 1
	}
	rec.FlaggedUntil = round + params.AimWindowRounds
	rec.Flags++
	if err := k.NodeRecord.Set(ctx, msg.NodeId, rec); err != nil {
		return nil, err
	}
	emit(ctx, EventTypeFlag,
		sdk.NewAttribute(AttributeKeyNodeID, u64(msg.NodeId)),
		sdk.NewAttribute(AttributeKeyRound, u64(rec.FlaggedUntil)),
	)
	return &types.MsgFlagNodeResponse{Until: rec.FlaggedUntil}, nil
}
