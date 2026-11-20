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
//
// The signer, in the operator field, is either the node's operator or its
// service key. Payment always goes to the operator.
func (k msgServer) RedeemTickets(ctx context.Context, msg *types.MsgRedeemTickets) (*types.MsgRedeemTicketsResponse, error) {
	signer, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid signer address")
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	count := uint64(len(msg.Tickets))
	if count == 0 || count > params.MaxBatch {
		return nil, errorsmod.Wrapf(types.ErrInvalidBatch, "got %d tickets, want 1-%d", count, params.MaxBatch)
	}

	// Only the operator of a registered, active node can be paid, and only
	// the operator or the node's service key may ask.
	node, err := k.registryKeeper.GetNode(ctx, msg.NodeId)
	if err != nil {
		return nil, err
	}
	operator, err := k.addressCodec.StringToBytes(node.Operator)
	if err != nil {
		return nil, errorsmod.Wrapf(types.ErrNotNodeOperator, "node %d has an invalid operator address", msg.NodeId)
	}
	allowed := bytes.Equal(signer, operator)
	if !allowed && node.ServiceKey != "" {
		service, err := k.addressCodec.StringToBytes(node.ServiceKey)
		allowed = err == nil && bytes.Equal(signer, service)
	}
	if !allowed {
		return nil, errorsmod.Wrapf(types.ErrNotNodeOperator, "node %d: signer is neither its operator nor its service key", msg.NodeId)
	}
	if node.Status != registrytypes.StatusActive {
		return nil, errorsmod.Wrapf(types.ErrNodeNotActive, "node %d", msg.NodeId)
	}

	if err := k.redeem(ctx, params, node.Id, operator, msg.KeyId, msg.Tickets); err != nil {
		return nil, err
	}
	return &types.MsgRedeemTicketsResponse{}, nil
}

// redeem checks and spends tickets made out to a node and pays its operator,
// less the protocol fee. If any ticket fails, nothing is paid.
func (k Keeper) redeem(ctx context.Context, params types.Params, nodeID uint64, operator sdk.AccAddress, keyID uint64, tickets []string) error {
	count := uint64(len(tickets))
	key, err := k.GetIssuerKey(ctx, keyID)
	if err != nil {
		return err
	}
	now, _, chainID := blockInfo(ctx)
	if now >= key.RedeemEnd {
		return errorsmod.Wrapf(types.ErrKeyExpired, "key %d", key.Id)
	}
	pubkey, err := decodeBase64(key.Pubkey)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvalidIssuerKey, "stored public key is not valid base64")
	}

	consumeGas(ctx, count*GasPerPairingCheck, "tickets: verify tickets")
	for i, text := range tickets {
		raw, err := decodeBase64(text)
		if err != nil {
			return errorsmod.Wrapf(types.ErrInvalidTicket, "ticket %d: not valid base64", i)
		}
		ticketKey, err := ticketcrypto.VerifyTicket(pubkey, chainID, key.Id, nodeID, raw)
		if err != nil {
			return errorsmod.Wrapf(types.ErrInvalidTicket, "ticket %d: %s", i, err)
		}

		spentKey := collections.Join(key.Id, ticketKey)
		spent, err := k.Spent.Has(ctx, spentKey)
		if err != nil {
			return err
		}
		if spent {
			return errorsmod.Wrapf(types.ErrTicketSpent, "ticket %d", i)
		}
		// Recording it now also rejects the same ticket twice in one batch.
		if err := k.Spent.Set(ctx, spentKey); err != nil {
			return err
		}
	}

	// Never pay out more tickets than the chain saw issued. This caps the
	// damage if issuer keys are ever misused to forge tickets.
	key.Redeemed += count
	if key.Redeemed > key.Issued {
		return errorsmod.Wrapf(types.ErrOverRedemption, "key %d: %d issued", key.Id, key.Issued)
	}
	if err := k.IssuerKey.Set(ctx, key.Id, key); err != nil {
		return err
	}

	total := key.Price.Amount.Mul(sdkmath.NewIntFromUint64(count))
	fee := total.Mul(sdkmath.NewIntFromUint64(params.FeeBps)).Quo(sdkmath.NewIntFromUint64(types.BasisPoints))
	payout := sdk.NewCoin(key.Price.Denom, total.Sub(fee))
	feeCoin := sdk.NewCoin(key.Price.Denom, fee)

	if err := k.distributeFee(ctx, feeCoin); err != nil {
		return err
	}
	if payout.IsPositive() {
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, operator, sdk.NewCoins(payout)); err != nil {
			return err
		}
	}

	emit(ctx, EventTypeRedeem,
		sdk.NewAttribute(AttributeKeyNodeID, u64(nodeID)),
		sdk.NewAttribute(AttributeKeyKeyID, u64(key.Id)),
		sdk.NewAttribute(AttributeKeyCount, u64(count)),
		sdk.NewAttribute(AttributeKeyAmount, payout.String()),
		sdk.NewAttribute(AttributeKeyFee, feeCoin.String()),
	)

	return nil
}

// RedeemForFlag spends one ticket made out to a node, paying its operator as
// any redeemed ticket would. A customer's flag on a node is backed by such a
// ticket: it shows the flagger could have been a customer, costs the
// flagger a ticket each time, and pays the flagged node rather than hurting
// it.
func (k Keeper) RedeemForFlag(ctx context.Context, nodeID, keyID uint64, ticket string) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	node, err := k.registryKeeper.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	if node.Status != registrytypes.StatusActive || node.Commitment != "" {
		return errorsmod.Wrapf(types.ErrNodeNotActive, "node %d", nodeID)
	}
	operator, err := k.addressCodec.StringToBytes(node.Operator)
	if err != nil {
		return errorsmod.Wrapf(types.ErrNotNodeOperator, "node %d has an invalid operator address", nodeID)
	}
	return k.redeem(ctx, params, nodeID, operator, keyID, []string{ticket})
}
