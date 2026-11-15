package keeper

import (
	"context"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	for _, elem := range genState.ProberList {
		if err := k.Prober.Set(ctx, elem.Id, elem); err != nil {
			return err
		}
	}

	if err := k.ProberSeq.Set(ctx, genState.ProberCount); err != nil {
		return err
	}
	return k.Params.Set(ctx, genState.Params)
}

// ExportGenesis returns the module's exported genesis.
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

	return genesis, nil
}
