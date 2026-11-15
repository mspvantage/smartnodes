package keeper

import (
	"bytes"
	"context"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// RedeemTickets pays a node for a batch of tickets. For every ticket the
// chain checks that the issuers signed it, that its holder named this node,
// and that it has not been paid before. If any ticket fails, the whole
// transaction fails and nothing is paid.
func (k msgServer) RedeemTickets(ctx context.Context, msg *types.MsgRedeemTickets) (*types.MsgRedeemTicketsResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	count := uint64(len(msg.Tickets))
	if count == 0 || count > params.MaxBatch {
		return nil, errorsmod.Wrapf(types.ErrInvalidBatch, "got %d tickets, want 1-%d", count, params.MaxBatch)
	}

	// Only the operator of a registered, active node can be paid.
	node, err := k.registryKeeper.GetNode(ctx, msg.NodeId)
	if err != nil {
		return nil, err
	}
	owner, err := k.addressCodec.StringToBytes(node.Operator)
	if err != nil || !bytes.Equal(owner, operator) {
		return nil, errorsmod.Wrapf(types.ErrNotNodeOperator, "node %d", msg.NodeId)
	}
	if node.Status != registrytypes.StatusActive {
		return nil, errorsmod.Wrapf(types.ErrNodeNotActive, "node %d", msg.NodeId)
	}

	key, err := k.GetIssuerKey(ctx, msg.KeyId)
	if err != nil {
		return nil, err
	}
	now, _, chainID := blockInfo(ctx)
	if now >= key.RedeemEnd {
		return nil, errorsmod.Wrapf(types.ErrKeyExpired, "key %d", key.Id)
	}
	pubkey, err := decodeBase64(key.Pubkey)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidIssuerKey, "stored public key is not valid base64")
	}

	consumeGas(ctx, count*GasPerPairingCheck, "tickets: verify tickets")
	for i, text := range msg.Tickets {
		raw, err := decodeBase64(text)
		if err != nil {
			return nil, errorsmod.Wrapf(types.ErrInvalidTicket, "ticket %d: not valid base64", i)
		}
		ticketKey, err := ticketcrypto.VerifyTicket(pubkey, chainID, key.Id, msg.NodeId, raw)
		if err != nil {
			return nil, errorsmod.Wrapf(types.ErrInvalidTicket, "ticket %d: %s", i, err)
		}

		spentKey := collections.Join(key.Id, ticketKey)
		spent, err := k.Spent.Has(ctx, spentKey)
		if err != nil {
			return nil, err
		}
		if spent {
			return nil, errorsmod.Wrapf(types.ErrTicketSpent, "ticket %d", i)
		}
		// Recording it now also rejects the same ticket twice in one batch.
		if err := k.Spent.Set(ctx, spentKey); err != nil {
			return nil, err
		}
	}

	// Never pay out more tickets than the chain saw issued. This caps the
	// damage if issuer keys are ever misused to forge tickets.
	key.Redeemed += count
	if key.Redeemed > key.Issued {
		return nil, errorsmod.Wrapf(types.ErrOverRedemption, "key %d: %d issued", key.Id, key.Issued)
	}
	if err := k.IssuerKey.Set(ctx, key.Id, key); err != nil {
		return nil, err
	}

	total := key.Price.Amount.Mul(sdkmath.NewIntFromUint64(count))
	fee := total.Mul(sdkmath.NewIntFromUint64(params.FeeBps)).Quo(sdkmath.NewIntFromUint64(types.BasisPoints))
	payout := sdk.NewCoin(key.Price.Denom, total.Sub(fee))
	feeCoin := sdk.NewCoin(key.Price.Denom, fee)

	if err := k.distributeFee(ctx, feeCoin); err != nil {
		return nil, err
	}
	if payout.IsPositive() {
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, operator, sdk.NewCoins(payout)); err != nil {
			return nil, err
		}
	}

	emit(ctx, EventTypeRedeem,
		sdk.NewAttribute(AttributeKeyNodeID, u64(msg.NodeId)),
		sdk.NewAttribute(AttributeKeyKeyID, u64(key.Id)),
		sdk.NewAttribute(AttributeKeyCount, u64(count)),
		sdk.NewAttribute(AttributeKeyAmount, payout.String()),
		sdk.NewAttribute(AttributeKeyFee, feeCoin.String()),
	)

	return &types.MsgRedeemTicketsResponse{}, nil
}
