package keeper

import (
	"bytes"
	"context"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// AddIssuerKey registers the public key that signs tickets for one period.
// Only the module authority (governance) may call it.
//
// The ticket price is fixed per key, so a later price change never alters
// the value of tickets already sold.
func (k msgServer) AddIssuerKey(ctx context.Context, msg *types.MsgAddIssuerKey) (*types.MsgAddIssuerKeyResponse, error) {
	authority, err := k.addressCodec.StringToBytes(msg.Authority)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid authority address")
	}
	if !bytes.Equal(k.GetAuthority(), authority) {
		expected, _ := k.addressCodec.BytesToString(k.GetAuthority())
		return nil, errorsmod.Wrapf(types.ErrInvalidSigner, "invalid authority; expected %s, got %s", expected, msg.Authority)
	}

	pubkey, err := decodeBase64(msg.Pubkey)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidIssuerKey, "public key is not valid base64")
	}
	if err := ticketcrypto.ValidatePublicKey(pubkey); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidIssuerKey, err.Error())
	}

	if err := msg.Price.Validate(); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidIssuerKey, err.Error())
	}
	if !msg.Price.IsPositive() {
		return nil, errorsmod.Wrap(types.ErrInvalidIssuerKey, "ticket price must be greater than zero")
	}

	now, _, _ := blockInfo(ctx)
	if msg.SignEnd <= now {
		return nil, errorsmod.Wrap(types.ErrInvalidIssuerKey, "sign_end must be in the future")
	}
	if msg.RedeemEnd <= msg.SignEnd {
		return nil, errorsmod.Wrap(types.ErrInvalidIssuerKey, "redeem_end must be after sign_end")
	}

	id, err := k.IssuerKeySeq.Next(ctx)
	if err != nil {
		return nil, err
	}
	key := types.IssuerKey{
		Id:        id,
		Pubkey:    msg.Pubkey,
		Price:     msg.Price,
		SignEnd:   msg.SignEnd,
		RedeemEnd: msg.RedeemEnd,
	}
	if err := k.IssuerKey.Set(ctx, id, key); err != nil {
		return nil, err
	}
	// Queue the key for the end-of-life sweep at its redemption deadline.
	if err := k.KeyExpiry.Set(ctx, collections.Join(msg.RedeemEnd, id)); err != nil {
		return nil, err
	}

	emit(ctx, EventTypeAddIssuerKey,
		sdk.NewAttribute(AttributeKeyKeyID, u64(id)),
		sdk.NewAttribute(AttributeKeyAmount, msg.Price.String()),
	)

	return &types.MsgAddIssuerKeyResponse{Id: id}, nil
}
