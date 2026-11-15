package keeper

import (
	"context"
	"errors"
	"math"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// EndBlocker runs the module's end-of-block jobs:
//
//  1. refund unsigned purchases and delete old signed ones,
//  2. sweep issuer keys that have passed their redemption deadline,
//  3. delete spent-ticket records of swept keys, a bounded number per block.
//
// Each job bounds its own work, and none lets a single bad entry stop the
// chain.
func (k Keeper) EndBlocker(ctx context.Context) error {
	if err := k.ProcessQueue(ctx); err != nil {
		return err
	}
	if err := k.SweepExpiredKeys(ctx); err != nil {
		return err
	}
	return k.ClearSpent(ctx)
}

// SweepExpiredKeys handles issuer keys whose redemption deadline has passed.
// Their tickets can no longer be redeemed, and being anonymous they cannot be
// refunded, so the value of the unspent ones moves to the community pool. The
// key's spent-ticket records are then queued for deletion.
func (k Keeper) SweepExpiredKeys(ctx context.Context) error {
	now, _, _ := blockInfo(ctx)

	due := new(collections.Range[collections.Pair[int64, uint64]]).
		EndInclusive(collections.Join(now, uint64(math.MaxUint64)))
	iter, err := k.KeyExpiry.Iterate(ctx, due)
	if err != nil {
		return err
	}
	var entries []collections.Pair[int64, uint64]
	for ; iter.Valid() && len(entries) < MaxKeySweepsPerBlock; iter.Next() {
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
		if err := k.KeyExpiry.Remove(ctx, entry); err != nil {
			return err
		}

		key, err := k.IssuerKey.Get(ctx, id)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				continue
			}
			return err
		}
		// The queue entry may be stale if the deadline it was filed under is
		// not the key's current one.
		if now < key.RedeemEnd {
			if err := k.KeyExpiry.Set(ctx, collections.Join(key.RedeemEnd, id)); err != nil {
				return err
			}
			continue
		}
		swept, err := k.SweptKeys.Has(ctx, id)
		if err != nil {
			return err
		}
		if swept {
			continue
		}

		unspentValue := sdk.NewCoin(key.Price.Denom, sdkmath.ZeroInt())
		if key.Issued > key.Redeemed {
			unspent := sdkmath.NewIntFromUint64(key.Issued - key.Redeemed)
			unspentValue = sdk.NewCoin(key.Price.Denom, key.Price.Amount.Mul(unspent))
		}
		if unspentValue.IsPositive() {
			if err := k.distrKeeper.FundCommunityPool(ctx, sdk.NewCoins(unspentValue), moduleAddress()); err != nil {
				// Leave the key unswept, so its value stays accounted for,
				// and try again later.
				emit(ctx, EventTypeSweepFailed,
					sdk.NewAttribute(AttributeKeyKeyID, u64(id)),
					sdk.NewAttribute(AttributeKeyErrorReason, err.Error()),
				)
				if err := k.KeyExpiry.Set(ctx, collections.Join(now+SweepRetrySeconds, id)); err != nil {
					return err
				}
				continue
			}
		}

		if err := k.SweptKeys.Set(ctx, id); err != nil {
			return err
		}
		if err := k.Clearing.Set(ctx, id); err != nil {
			return err
		}
		emit(ctx, EventTypeKeySwept,
			sdk.NewAttribute(AttributeKeyKeyID, u64(id)),
			sdk.NewAttribute(AttributeKeyAmount, unspentValue.String()),
		)
	}

	return nil
}

// ClearSpent deletes spent-ticket records of swept keys. A key can have
// millions of them, so at most MaxSpentDeletesPerBlock are removed per block.
// The records are safe to delete because redemption under a key past its
// deadline is refused before the spent list is consulted.
func (k Keeper) ClearSpent(ctx context.Context) error {
	budget := MaxSpentDeletesPerBlock

	for budget > 0 {
		// Take the first key still being cleared.
		keyIter, err := k.Clearing.Iterate(ctx, nil)
		if err != nil {
			return err
		}
		if !keyIter.Valid() {
			return keyIter.Close()
		}
		id, err := keyIter.Key()
		if err != nil {
			_ = keyIter.Close()
			return err
		}
		if err := keyIter.Close(); err != nil {
			return err
		}

		spentIter, err := k.Spent.Iterate(ctx, collections.NewPrefixedPairRange[uint64, []byte](id))
		if err != nil {
			return err
		}
		var batch []collections.Pair[uint64, []byte]
		for ; spentIter.Valid() && len(batch) < budget; spentIter.Next() {
			entry, err := spentIter.Key()
			if err != nil {
				_ = spentIter.Close()
				return err
			}
			batch = append(batch, entry)
		}
		exhausted := !spentIter.Valid()
		if err := spentIter.Close(); err != nil {
			return err
		}

		for _, entry := range batch {
			if err := k.Spent.Remove(ctx, entry); err != nil {
				return err
			}
		}
		budget -= len(batch)

		if exhausted {
			// Nothing is left under this key: stop tracking it.
			if err := k.Clearing.Remove(ctx, id); err != nil {
				return err
			}
		}
	}

	return nil
}
