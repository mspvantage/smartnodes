package keeper

import (
	"context"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
//
// Only probers, node records and parameters are stored in genesis. The owner
// index, the active list and the waiting lists are rebuilt here from each
// prober's status, in order of prober id, so every validator rebuilds the
// same lists. Rounds start again from 1.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	for _, elem := range genState.ProberList {
		if err := k.Prober.Set(ctx, elem.Id, elem); err != nil {
			return err
		}
	}
	if err := k.ProberSeq.Set(ctx, genState.ProberCount); err != nil {
		return err
	}

	// Walk the stored probers so the order is by id whatever order the
	// genesis file listed them in.
	err := k.Prober.Walk(ctx, nil, func(id uint64, p types.Prober) (bool, error) {
		if p.Owner != "" {
			if err := k.ProberByOwner.Set(ctx, p.Owner, id); err != nil {
				return true, err
			}
		}
		switch p.Status {
		case types.ProberPending:
			return false, k.PendingJoin.Set(ctx, id)
		case types.ProberActive:
			return false, k.addActive(ctx, id)
		case types.ProberLeaving:
			if err := k.addActive(ctx, id); err != nil {
				return true, err
			}
			return false, k.PendingLeave.Set(ctx, id)
		}
		return false, nil
	})
	if err != nil {
		return err
	}

	for _, rec := range genState.NodeRecordList {
		if err := k.NodeRecord.Set(ctx, rec.NodeId, rec); err != nil {
			return err
		}
	}

	return k.Params.Set(ctx, genState.Params)
}

// ExportGenesis returns the module's exported genesis.
//
// Round numbers, seeds and reports that have not been tallied are left out.
// A chain restarted from an export begins a fresh round, and reports in
// flight at the moment of export are lost.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	var err error

	genesis := types.DefaultGenesis()
	genesis.Params, err = k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	err = k.Prober.Walk(ctx, nil, func(key uint64, elem types.Prober) (bool, error) {
		genesis.ProberList = append(genesis.ProberList, elem)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	genesis.ProberCount, err = k.ProberSeq.Peek(ctx)
	if err != nil {
		return nil, err
	}

	err = k.NodeRecord.Walk(ctx, nil, func(_ uint64, rec types.NodeRecord) (bool, error) {
		genesis.NodeRecordList = append(genesis.NodeRecordList, rec)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	return genesis, nil
}
