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
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

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

// Bounds on end-of-block work. Anything beyond them waits for the next block.
const (
	// MaxQueueEntriesPerBlock is the most refunds and purchase deletions.
	MaxQueueEntriesPerBlock = 200
	// MaxKeySweepsPerBlock is the most expired issuer keys swept.
	MaxKeySweepsPerBlock = 5
	// MaxSpentDeletesPerBlock is the most spent-ticket records deleted.
	MaxSpentDeletesPerBlock = 500
	// SweepRetrySeconds is how long to wait before retrying a failed sweep.
	SweepRetrySeconds int64 = 3_600
)

// Event types and attribute keys emitted by the tickets module.
const (
	EventTypeAddIssuerKey   = "tickets_add_issuer_key"
	EventTypeBuy            = "tickets_buy"
	EventTypeFulfil         = "tickets_fulfil"
	EventTypeRedeem         = "tickets_redeem"
	EventTypeRefund         = "tickets_refund"
	EventTypeRefundFailed   = "tickets_refund_failed"
	EventTypeKeySwept       = "tickets_key_swept"
	EventTypeSweepFailed    = "tickets_key_sweep_failed"
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

// moduleAddress is the tickets escrow account.
func moduleAddress() sdk.AccAddress {
	return authtypes.NewModuleAddress(types.ModuleName)
}

// addProberFund records coins held for the verification module. They stay in
// the tickets module account.
func (k Keeper) addProberFund(ctx context.Context, amount sdk.Coin) error {
	if !amount.IsPositive() {
		return nil
	}
	current, err := k.ProberFund.Get(ctx, amount.Denom)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		current = sdkmath.ZeroInt()
	}
	return k.ProberFund.Set(ctx, amount.Denom, current.Add(amount.Amount))
}

// distributeFee divides a protocol fee three ways: stakers, the community
// pool and the prober fund. The prober fund takes the remainder, so the three
// parts always add up to the fee exactly.
func (k Keeper) distributeFee(ctx context.Context, fee sdk.Coin) error {
	if !fee.IsPositive() {
		return nil
	}
	bp := sdkmath.NewIntFromUint64(types.BasisPoints)
	stakers := fee.Amount.Mul(sdkmath.NewIntFromUint64(types.FeeShareStakersBps)).Quo(bp)
	community := fee.Amount.Mul(sdkmath.NewIntFromUint64(types.FeeShareCommunityBps)).Quo(bp)
	prober := fee.Amount.Sub(stakers).Sub(community)

	if stakers.IsPositive() {
		coins := sdk.NewCoins(sdk.NewCoin(fee.Denom, stakers))
		if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.ModuleName, authtypes.FeeCollectorName, coins); err != nil {
			return err
		}
	}
	if community.IsPositive() {
		coins := sdk.NewCoins(sdk.NewCoin(fee.Denom, community))
		if err := k.distrKeeper.FundCommunityPool(ctx, coins, moduleAddress()); err != nil {
			return err
		}
	}
	return k.addProberFund(ctx, sdk.NewCoin(fee.Denom, prober))
}

// FundProberFund moves coins from another module account into the prober
// fund. The coins and the fund's record move together, so the escrow always
// matches the module's records.
func (k Keeper) FundProberFund(ctx context.Context, fromModule string, amount sdk.Coin) error {
	if err := amount.Validate(); err != nil {
		return err
	}
	if !amount.IsPositive() {
		return nil
	}
	if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, fromModule, types.ModuleName, sdk.NewCoins(amount)); err != nil {
		return err
	}
	return k.addProberFund(ctx, amount)
}

// PayFromProberFund pays a prober out of the prober fund. It pays the
// smaller of the amount asked for and what the fund holds in that coin, and
// returns what was paid, which may be zero. The fund's record and the escrow
// account move together, so the escrow always matches the module's records.
func (k Keeper) PayFromProberFund(ctx context.Context, recipient sdk.AccAddress, want sdk.Coin) (sdk.Coin, error) {
	zero := sdk.NewCoin(want.Denom, sdkmath.ZeroInt())
	if err := want.Validate(); err != nil {
		return zero, err
	}
	if !want.IsPositive() {
		return zero, nil
	}
	fund, err := k.ProberFund.Get(ctx, want.Denom)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return zero, nil
		}
		return zero, err
	}
	pay := sdk.NewCoin(want.Denom, sdkmath.MinInt(want.Amount, fund))
	if !pay.IsPositive() {
		return zero, nil
	}
	if err := k.ProberFund.Set(ctx, want.Denom, fund.Sub(pay.Amount)); err != nil {
		return zero, err
	}
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, recipient, sdk.NewCoins(pay)); err != nil {
		return zero, err
	}
	return pay, nil
}

// EscrowExpected returns what the tickets module account must hold:
//
//	payments locked in unfulfilled purchases
//	+ the value of tickets issued and not yet redeemed, for keys not yet swept
//	+ the prober fund
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
		// Once a key is swept, its unspent value has left for the community pool.
		swept, err := k.SweptKeys.Has(ctx, key.Id)
		if err != nil {
			return true, err
		}
		if !swept && key.Issued > key.Redeemed {
			outstanding := sdkmath.NewIntFromUint64(key.Issued - key.Redeemed)
			total = total.Add(sdk.NewCoin(key.Price.Denom, key.Price.Amount.Mul(outstanding)))
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	err = k.ProberFund.Walk(ctx, nil, func(denom string, amount sdkmath.Int) (bool, error) {
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
