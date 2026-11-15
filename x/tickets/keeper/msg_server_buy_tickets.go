package keeper

import (
	"context"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// BuyTickets locks payment for a batch of blinded tickets and records the
// request for the issuers to sign. If they do not sign before the deadline,
// the payment is refunded at the end of that block.
func (k msgServer) BuyTickets(ctx context.Context, msg *types.MsgBuyTickets) (*types.MsgBuyTicketsResponse, error) {
	buyer, err := k.addressCodec.StringToBytes(msg.Buyer)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid buyer address")
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	count := uint64(len(msg.Blinded))
	if count == 0 || count > params.MaxBatch {
		return nil, errorsmod.Wrapf(types.ErrInvalidBatch, "got %d tickets, want 1-%d", count, params.MaxBatch)
	}

	key, err := k.GetIssuerKey(ctx, msg.KeyId)
	if err != nil {
		return nil, err
	}
	now, height, _ := blockInfo(ctx)
	if now >= key.SignEnd {
		return nil, errorsmod.Wrapf(types.ErrKeyNotSigning, "key %d", key.Id)
	}

	consumeGas(ctx, count*GasPerPointCheck, "tickets: check blinded values")
	seen := make(map[string]struct{}, count)
	for i, text := range msg.Blinded {
		raw, err := decodeBase64(text)
		if err != nil {
			return nil, errorsmod.Wrapf(types.ErrInvalidBlinded, "ticket %d: not valid base64", i)
		}
		if err := ticketcrypto.ValidateBlinded(raw); err != nil {
			return nil, errorsmod.Wrapf(types.ErrInvalidBlinded, "ticket %d: %s", i, err)
		}
		if _, dup := seen[string(raw)]; dup {
			return nil, errorsmod.Wrapf(types.ErrDuplicateBlinded, "ticket %d", i)
		}
		seen[string(raw)] = struct{}{}
	}

	cost := sdk.NewCoin(key.Price.Denom, key.Price.Amount.Mul(sdkmath.NewIntFromUint64(count)))
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, buyer, types.ModuleName, sdk.NewCoins(cost)); err != nil {
		return nil, err
	}

	id, err := k.PurchaseSeq.Next(ctx)
	if err != nil {
		return nil, err
	}
	buyerStr, err := k.addressCodec.BytesToString(buyer)
	if err != nil {
		return nil, err
	}
	deadline := height + int64(params.IssuanceDeadlineBlocks)
	purchase := types.Purchase{
		Id:       id,
		Buyer:    buyerStr,
		KeyId:    key.Id,
		Blinded:  msg.Blinded,
		Locked:   cost,
		Deadline: deadline,
	}
	if err := k.Purchase.Set(ctx, id, purchase); err != nil {
		return nil, err
	}
	if err := k.Queue.Set(ctx, collections.Join(deadline, id)); err != nil {
		return nil, err
	}

	emit(ctx, EventTypeBuy,
		sdk.NewAttribute(AttributeKeyPurchaseID, u64(id)),
		sdk.NewAttribute(AttributeKeyKeyID, u64(key.Id)),
		sdk.NewAttribute(AttributeKeyCount, u64(count)),
		sdk.NewAttribute(AttributeKeyAmount, cost.String()),
	)

	return &types.MsgBuyTicketsResponse{Id: id}, nil
}
