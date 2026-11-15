package keeper

import (
	"context"

	"cosmossdk.io/collections"

	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
//
// KNOWN GAP: the list of spent tickets and the accrued fees are not part of
// the genesis format yet. A chain restarted from an exported genesis would
// forget which tickets were redeemed. This must be closed before the module
// is used on any network that upgrades by export.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	for _, elem := range genState.IssuerKeyList {
		if err := k.IssuerKey.Set(ctx, elem.Id, elem); err != nil {
			return err
		}
	}

	if err := k.IssuerKeySeq.Set(ctx, genState.IssuerKeyCount); err != nil {
		return err
	}
	params := genState.Params
	for _, elem := range genState.PurchaseList {
		if err := k.Purchase.Set(ctx, elem.Id, elem); err != nil {
			return err
		}
		// Rebuild the end-of-block queue: a refund deadline for an unsigned
		// purchase, a deletion time for a signed one.
		due := elem.Deadline
		if IsFulfilled(elem) {
			due = elem.FulfilledAt + int64(params.RetentionBlocks)
		}
		if err := k.Queue.Set(ctx, collections.Join(due, elem.Id)); err != nil {
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
