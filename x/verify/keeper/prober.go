package keeper

import (
	"context"
	"errors"
	"strconv"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// Event types and attribute keys emitted by the verify module.
const (
	EventTypeRegisterProber   = "verify_register_prober"
	EventTypeDeregisterProber = "verify_deregister_prober"
	EventTypeWithdrawProber   = "verify_withdraw_prober_bond"
	EventTypeProberJoined     = "verify_prober_joined"
	EventTypeProberLeft       = "verify_prober_left"
	EventTypeNewRound         = "verify_new_round"
	EventTypeCommit           = "verify_commit"
	EventTypeReveal           = "verify_reveal"
	EventTypeTally            = "verify_tally"
	EventTypeWouldJail        = "verify_would_jail"
	EventTypeBlockerFailed    = "verify_begin_block_failed"
	EventTypeTallyFailed      = "verify_tally_failed"
	EventTypeJail             = "verify_jail"
	EventTypeUnjail           = "verify_unjail"
	EventTypeSlash            = "verify_slash"
	EventTypeWouldSlash       = "verify_would_slash"
	EventTypeReward           = "verify_reward"
	EventTypeRewardFailed     = "verify_reward_failed"

	AttributeKeyProberID     = "prober_id"
	AttributeKeyOwner        = "owner"
	AttributeKeyAmount       = "amount"
	AttributeKeyUnbondingEnd = "unbonding_end"
	AttributeKeyRound        = "round"
	AttributeKeySeed         = "seed"
	AttributeKeyActive       = "active_probers"
	AttributeKeyNodeID       = "node_id"
	AttributeKeyCount        = "count"
	AttributeKeyResult       = "result"
	AttributeKeyOutcome      = "outcome"
	AttributeKeyPass         = "pass"
	AttributeKeyFail         = "fail"
	AttributeKeyTamper       = "tamper"
	AttributeKeySilent       = "silent"
	AttributeKeyFails        = "consecutive_fails"
	AttributeKeyReason       = "reason"
)

func u64(v uint64) string { return strconv.FormatUint(v, 10) }

func emit(ctx context.Context, eventType string, attrs ...sdk.Attribute) {
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(eventType, attrs...))
}

func consumeGas(ctx context.Context, amount uint64, descriptor string) {
	sdk.UnwrapSDKContext(ctx).GasMeter().ConsumeGas(amount, descriptor)
}

// blockUnix returns the current block time as Unix seconds. Block time comes
// from consensus, so every validator sees the same value.
func blockUnix(ctx context.Context) int64 {
	return sdk.UnwrapSDKContext(ctx).BlockTime().Unix()
}

// activeCount returns the length of the active list, zero if never set.
func (k Keeper) activeCount(ctx context.Context) (uint64, error) {
	n, err := k.ActiveCount.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return n, nil
}

// proberByOwner loads the prober owned by the given address text.
func (k Keeper) proberByOwner(ctx context.Context, owner string) (types.Prober, error) {
	id, err := k.ProberByOwner.Get(ctx, owner)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Prober{}, errorsmod.Wrapf(types.ErrProberNotFound, "owner %s", owner)
		}
		return types.Prober{}, err
	}
	prober, err := k.Prober.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Prober{}, errorsmod.Wrapf(types.ErrProberNotFound, "id %d", id)
		}
		return types.Prober{}, err
	}
	return prober, nil
}

// bondCoins turns a bond amount into coins of the staking token.
func (k Keeper) bondCoins(ctx context.Context, amount uint64) (sdk.Coins, error) {
	denom, err := k.registryKeeper.BondDenom(ctx)
	if err != nil {
		return nil, err
	}
	return sdk.NewCoins(sdk.NewCoin(denom, math.NewIntFromUint64(amount))), nil
}

// TotalBonded returns the sum of all prober bonds. The verify module account
// must always hold exactly this amount of the staking token.
func (k Keeper) TotalBonded(ctx context.Context) (math.Int, error) {
	total := math.ZeroInt()
	err := k.Prober.Walk(ctx, nil, func(_ uint64, p types.Prober) (bool, error) {
		total = total.Add(math.NewIntFromUint64(p.Bond))
		return false, nil
	})
	return total, err
}

// addActive appends a prober to the active list.
func (k Keeper) addActive(ctx context.Context, id uint64) error {
	count, err := k.activeCount(ctx)
	if err != nil {
		return err
	}
	if err := k.ActiveList.Set(ctx, count, id); err != nil {
		return err
	}
	if err := k.ActiveIndex.Set(ctx, id, count); err != nil {
		return err
	}
	return k.ActiveCount.Set(ctx, count+1)
}

// removeActive takes a prober out of the active list. The last prober in the
// list moves into the gap, so the list stays dense and removal costs the same
// however long the list is.
func (k Keeper) removeActive(ctx context.Context, id uint64) error {
	idx, err := k.ActiveIndex.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	count, err := k.activeCount(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("active index exists but the active list is empty")
	}
	last := count - 1
	if idx != last {
		moved, err := k.ActiveList.Get(ctx, last)
		if err != nil {
			return err
		}
		if err := k.ActiveList.Set(ctx, idx, moved); err != nil {
			return err
		}
		if err := k.ActiveIndex.Set(ctx, moved, idx); err != nil {
			return err
		}
	}
	if err := k.ActiveList.Remove(ctx, last); err != nil {
		return err
	}
	if err := k.ActiveIndex.Remove(ctx, id); err != nil {
		return err
	}
	return k.ActiveCount.Set(ctx, last)
}

// firstKeys returns up to limit keys from a key set, lowest first.
func firstKeys(ctx context.Context, set collections.KeySet[uint64], limit int) ([]uint64, error) {
	var keys []uint64
	err := set.Walk(ctx, nil, func(key uint64) (bool, error) {
		keys = append(keys, key)
		return len(keys) >= limit, nil
	})
	return keys, err
}

// applySetChanges moves waiting probers into and out of the active set. It
// runs once, when a round starts, so the set is fixed for the whole round and
// every assignment check during the round sees the same list.
//
// The work is bounded. If more probers are waiting than the bound, the rest
// are handled when the following round starts.
func (k Keeper) applySetChanges(ctx context.Context) error {
	leaving, err := firstKeys(ctx, k.PendingLeave, MaxSetChangesPerRound)
	if err != nil {
		return err
	}
	for _, id := range leaving {
		if err := k.PendingLeave.Remove(ctx, id); err != nil {
			return err
		}
		if err := k.removeActive(ctx, id); err != nil {
			return err
		}
		prober, err := k.Prober.Get(ctx, id)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				continue
			}
			return err
		}
		prober.Status = types.ProberUnbonding
		if err := k.Prober.Set(ctx, id, prober); err != nil {
			return err
		}
		emit(ctx, EventTypeProberLeft, sdk.NewAttribute(AttributeKeyProberID, u64(id)))
	}

	joining, err := firstKeys(ctx, k.PendingJoin, MaxSetChangesPerRound)
	if err != nil {
		return err
	}
	for _, id := range joining {
		if err := k.PendingJoin.Remove(ctx, id); err != nil {
			return err
		}
		prober, err := k.Prober.Get(ctx, id)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				continue
			}
			return err
		}
		if prober.Status != types.ProberPending {
			continue
		}
		if err := k.addActive(ctx, id); err != nil {
			return err
		}
		prober.Status = types.ProberActive
		if err := k.Prober.Set(ctx, id, prober); err != nil {
			return err
		}
		emit(ctx, EventTypeProberJoined, sdk.NewAttribute(AttributeKeyProberID, u64(id)))
	}
	return nil
}

// isJailed reports whether a node is jailed. A node with no record is not.
func (k Keeper) isJailed(ctx context.Context, nodeID uint64) (bool, error) {
	rec, err := k.NodeRecord.Get(ctx, nodeID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return rec.Jailed, nil
}
