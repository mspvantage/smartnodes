package keeper

import (
	"context"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// FulfilPurchase posts the issuers' blind signatures for a purchase. Anyone
// may send it: the chain checks every signature against the issuer public
// key, so a wrong signature is rejected whoever submits it.
//
// Once accepted, the buyer's payment stops being refundable and becomes the
// backing for the tickets issued.
func (k msgServer) FulfilPurchase(ctx context.Context, msg *types.MsgFulfilPurchase) (*types.MsgFulfilPurchaseResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Sender); err != nil {
		return nil, errorsmod.Wrap(err, "invalid sender address")
	}

	purchase, err := k.Purchase.Get(ctx, msg.Id)
	if err != nil {
		return nil, errorsmod.Wrapf(types.ErrPurchaseNotFound, "id %d", msg.Id)
	}
	if IsFulfilled(purchase) {
		return nil, errorsmod.Wrapf(types.ErrAlreadyFulfilled, "id %d", msg.Id)
	}
	count := uint64(len(purchase.Blinded))
	if uint64(len(msg.Signatures)) != count {
		return nil, errorsmod.Wrapf(types.ErrInvalidBatch, "got %d signatures for %d tickets", len(msg.Signatures), count)
	}

	key, err := k.GetIssuerKey(ctx, purchase.KeyId)
	if err != nil {
		return nil, err
	}
	// Tickets issued after the redemption deadline could never be spent, and
	// the key's unspent value may already have been swept. The purchase is
	// refunded at its own deadline instead.
	now, height, _ := blockInfo(ctx)
	if now >= key.RedeemEnd {
		return nil, errorsmod.Wrapf(types.ErrKeyExpired, "key %d", key.Id)
	}
	pubkey, err := decodeBase64(key.Pubkey)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidIssuerKey, "stored public key is not valid base64")
	}

	consumeGas(ctx, count*GasPerPairingCheck, "tickets: verify blind signatures")
	for i, text := range msg.Signatures {
		sig, err := decodeBase64(text)
		if err != nil {
			return nil, errorsmod.Wrapf(types.ErrBadBlindSignature, "signature %d: not valid base64", i)
		}
		blinded, err := decodeBase64(purchase.Blinded[i])
		if err != nil {
			return nil, errorsmod.Wrapf(types.ErrInvalidBlinded, "stored ticket %d: not valid base64", i)
		}
		if err := ticketcrypto.VerifyBlindSignature(pubkey, blinded, sig); err != nil {
			return nil, errorsmod.Wrapf(types.ErrBadBlindSignature, "signature %d: %s", i, err)
		}
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	// Swap the refund deadline for a deletion time.
	if err := k.Queue.Remove(ctx, collections.Join(purchase.Deadline, purchase.Id)); err != nil {
		return nil, err
	}
	if err := k.Queue.Set(ctx, collections.Join(height+int64(params.RetentionBlocks), purchase.Id)); err != nil {
		return nil, err
	}

	purchase.Signatures = msg.Signatures
	purchase.FulfilledAt = height
	if err := k.Purchase.Set(ctx, purchase.Id, purchase); err != nil {
		return nil, err
	}

	key.Issued += count
	if err := k.IssuerKey.Set(ctx, key.Id, key); err != nil {
		return nil, err
	}

	emit(ctx, EventTypeFulfil,
		sdk.NewAttribute(AttributeKeyPurchaseID, u64(purchase.Id)),
		sdk.NewAttribute(AttributeKeyKeyID, u64(key.Id)),
		sdk.NewAttribute(AttributeKeyCount, u64(count)),
	)

	return &types.MsgFulfilPurchaseResponse{}, nil
}
