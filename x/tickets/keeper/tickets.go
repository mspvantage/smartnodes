package keeper

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"strconv"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// Gas charged for cryptographic work, on top of the normal storage gas. One
// pairing check takes about a millisecond, far more than its bytes suggest.
const (
	// GasPerPointCheck covers decoding a curve point and checking its subgroup.
	GasPerPointCheck uint64 = 2_000
	// GasPerPairingCheck covers one blind-signature or ticket verification.
	GasPerPairingCheck uint64 = 20_000
)

// MaxQueueEntriesPerBlock bounds the end-of-block work. Anything beyond it
// waits for the next block.
const MaxQueueEntriesPerBlock = 200

// Event types and attribute keys emitted by the tickets module.
const (
	EventTypeAddIssuerKey   = "tickets_add_issuer_key"
	EventTypeBuy            = "tickets_buy"
	EventTypeFulfil         = "tickets_fulfil"
	EventTypeRedeem         = "tickets_redeem"
	EventTypeRefund         = "tickets_refund"
	EventTypeRefundFailed   = "tickets_refund_failed"
	AttributeKeyKeyID       = "key_id"
	AttributeKeyPurchaseID  = "purchase_id"
	AttributeKeyNodeID      = "node_id"
	AttributeKeyCount       = "count"
	AttributeKeyAmount      = "amount"
	AttributeKeyFee         = "fee"
	AttributeKeyBuyer       = "buyer"
	AttributeKeyErrorReason = "reason"
)

func u64(v uint64) string { return strconv.FormatUint(v, 10) }

// blockInfo returns the consensus-agreed time, height and chain id.
func blockInfo(ctx context.Context) (unix int64, height int64, chainID string) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.BlockTime().Unix(), sdkCtx.BlockHeight(), sdkCtx.ChainID()
}

func emit(ctx context.Context, eventType string, attrs ...sdk.Attribute) {
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(eventType, attrs...))
}

func consumeGas(ctx context.Context, amount uint64, descriptor string) {
	sdk.UnwrapSDKContext(ctx).GasMeter().ConsumeGas(amount, descriptor)
}

// decodeBase64 parses standard base64, the text form used for keys, blinded
// values, signatures and tickets in messages.
func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// IsFulfilled reports whether the issuers have signed a purchase.
func IsFulfilled(p types.Purchase) bool {
	return len(p.Signatures) > 0
}

// GetIssuerKey returns an issuer key by id, or ErrIssuerKeyNotFound.
func (k Keeper) GetIssuerKey(ctx context.Context, id uint64) (types.IssuerKey, error) {
	key, err := k.IssuerKey.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.IssuerKey{}, errorsmod.Wrapf(types.ErrIssuerKeyNotFound, "id %d", id)
		}
		return types.IssuerKey{}, err
	}
	return key, nil
}

// addAccruedFee records a protocol fee that stays in the module account.
func (k Keeper) addAccruedFee(ctx context.Context, fee sdk.Coin) error {
	if !fee.IsPositive() {
		return nil
	}
	current, err := k.AccruedFees.Get(ctx, fee.Denom)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		current = sdkmath.ZeroInt()
	}
	return k.AccruedFees.Set(ctx, fee.Denom, current.Add(fee.Amount))
}

// EscrowExpected returns what the tickets module account must hold:
//
//	payments locked in unfulfilled purchases
//	+ the value of tickets issued and not yet redeemed
//	+ protocol fees accrued and not yet distributed
//
// The module account balance must equal this at all times. Tests check it
// after every operation.
func (k Keeper) EscrowExpected(ctx context.Context) (sdk.Coins, error) {
	total := sdk.NewCoins()

	err := k.Purchase.Walk(ctx, nil, func(_ uint64, p types.Purchase) (bool, error) {
		if !IsFulfilled(p) {
			total = total.Add(p.Locked)
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	err = k.IssuerKey.Walk(ctx, nil, func(_ uint64, key types.IssuerKey) (bool, error) {
		if key.Issued > key.Redeemed {
			outstanding := sdkmath.NewIntFromUint64(key.Issued - key.Redeemed)
			total = total.Add(sdk.NewCoin(key.Price.Denom, key.Price.Amount.Mul(outstanding)))
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	err = k.AccruedFees.Walk(ctx, nil, func(denom string, amount sdkmath.Int) (bool, error) {
		if amount.IsPositive() {
			total = total.Add(sdk.NewCoin(denom, amount))
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	return total, nil
}

// ProcessQueue runs at the end of every block. For each queue entry that has
// come due it either refunds an unfulfilled purchase or deletes a fulfilled
// one whose signatures have been kept long enough.
//
// A failure on one entry must never stop the chain, so problems are recorded
// as events and the loop carries on.
func (k Keeper) ProcessQueue(ctx context.Context) error {
	_, height, _ := blockInfo(ctx)

	due := new(collections.Range[collections.Pair[int64, uint64]]).
		EndInclusive(collections.Join(height, uint64(math.MaxUint64)))

	iter, err := k.Queue.Iterate(ctx, due)
	if err != nil {
		return err
	}
	var entries []collections.Pair[int64, uint64]
	for ; iter.Valid() && len(entries) < MaxQueueEntriesPerBlock; iter.Next() {
		entry, err := iter.Key()
		if err != nil {
			_ = iter.Close()
			return err
		}
		entries = append(entries, entry)
	}
	if err := iter.Close(); err != nil {
		return err
	}

	for _, entry := range entries {
		id := entry.K2()
		if err := k.Queue.Remove(ctx, entry); err != nil {
			return err
		}

		purchase, err := k.Purchase.Get(ctx, id)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				continue
			}
			return err
		}

		if IsFulfilled(purchase) {
			// The buyer has had the retention period to read the signatures.
			if err := k.Purchase.Remove(ctx, id); err != nil {
				return err
			}
			continue
		}

		// Not signed in time: return the buyer's money.
		buyer, err := k.addressCodec.StringToBytes(purchase.Buyer)
		if err == nil {
			err = k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, buyer, sdk.NewCoins(purchase.Locked))
		}
		if err != nil {
			// Keep the record so the funds stay accounted for, and say why.
			emit(ctx, EventTypeRefundFailed,
				sdk.NewAttribute(AttributeKeyPurchaseID, u64(id)),
				sdk.NewAttribute(AttributeKeyErrorReason, err.Error()),
			)
			continue
		}
		if err := k.Purchase.Remove(ctx, id); err != nil {
			return err
		}
		emit(ctx, EventTypeRefund,
			sdk.NewAttribute(AttributeKeyPurchaseID, u64(id)),
			sdk.NewAttribute(AttributeKeyBuyer, purchase.Buyer),
			sdk.NewAttribute(AttributeKeyAmount, purchase.Locked.String()),
		)
	}

	return nil
}
