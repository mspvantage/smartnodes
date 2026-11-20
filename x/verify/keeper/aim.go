package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// fixAim fixes, for the round just started, each node's chance of being
// probed. It is fixed now, not read live, so nothing that happens during
// the round (a tally, an unjailing, a flag) can change which nodes were
// selected and leave a prober's assignment or sealed report invalid.
// A round's chances are kept until all its reports have been counted,
// because the reward for a pass depends on them.
func (k Keeper) fixAim(ctx sdk.Context, round uint64) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if err := k.forgetAim(ctx, round); err != nil {
		return err
	}
	return k.NodeRecord.Walk(ctx, nil, func(id uint64, rec types.NodeRecord) (bool, error) {
		return false, k.RoundAim.Set(ctx, collections.Join(round, id), types.AimedBps(params, &rec, round))
	})
}

// aimedBps is a node's chance of being probed in a round, as fixed when it
// started.
func (k Keeper) aimedBps(ctx context.Context, params types.Params, round, nodeID uint64) (uint64, error) {
	bps, err := k.RoundAim.Get(ctx, collections.Join(round, nodeID))
	if errors.Is(err, collections.ErrNotFound) {
		return types.AimedBps(params, nil, round), nil
	}
	return bps, err
}

// forgetAim drops the chances of rounds that can no longer matter: older
// than the previous round and older than any report still waiting to be
// counted.
func (k Keeper) forgetAim(ctx sdk.Context, round uint64) error {
	if round < 2 {
		return nil
	}
	keep := round - 1
	iter, err := k.Reports.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	if iter.Valid() {
		if key, err := iter.Key(); err == nil && key.K1() < keep {
			keep = key.K1()
		}
	}
	if err := iter.Close(); err != nil {
		return err
	}
	old := new(collections.Range[collections.Pair[uint64, uint64]]).EndExclusive(collections.Join(keep, uint64(0)))
	return k.RoundAim.Clear(ctx, old)
}

// PassWeight scales a pass's reward so that how often a node is probed does
// not change what it earns over time: a node probed twice as often earns
// half as much per pass. Being flagged, new or recently troubled therefore
// costs an honest node nothing, and a flag cannot be used to raise or cut a
// rival's income.
func PassWeight(capPerPass, sampleBps, aimed uint64) uint64 {
	if aimed == 0 || aimed == sampleBps {
		return capPerPass
	}
	w := math.NewIntFromUint64(capPerPass).
		Mul(math.NewIntFromUint64(sampleBps)).
		Quo(math.NewIntFromUint64(aimed))
	if !w.IsUint64() {
		return capPerPass
	}
	return w.Uint64()
}
