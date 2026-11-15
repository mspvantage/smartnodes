package keeper

import (
	"context"

	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	for _, elem := range genState.IssuerKeyList {
		if err := k.IssuerKey.Set(ctx, elem.Id, elem); err != nil {
			return err
		}
	}

	if err := k.IssuerKeySeq.Set(ctx, genState.IssuerKeyCount); err != nil {
		return err
	}
	for _, elem := range genState.PurchaseList {
		if err := k.Purchase.Set(ctx, elem.Id, elem); err != nil {
			return err
		}
	}

	if err := k.PurchaseSeq.Set(ctx, genState.PurchaseCount); err != nil {
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
	err = k.IssuerKey.Walk(ctx, nil, func(key uint64, elem types.IssuerKey) (bool, error) {
		genesis.IssuerKeyList = append(genesis.IssuerKeyList, elem)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	genesis.IssuerKeyCount, err = k.IssuerKeySeq.Peek(ctx)
	if err != nil {
		return nil, err
	}
	err = k.Purchase.Walk(ctx, nil, func(key uint64, elem types.Purchase) (bool, error) {
		genesis.PurchaseList = append(genesis.PurchaseList, elem)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	genesis.PurchaseCount, err = k.PurchaseSeq.Peek(ctx)
	if err != nil {
		return nil, err
	}

	return genesis, nil
}
