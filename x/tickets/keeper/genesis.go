package keeper

import (
	"context"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
//
// The genesis carries everything needed to keep the module's promises across
// a restart from an export: spent tickets (so none is paid twice), the prober
// fund, and which issuer keys have been swept. The end-of-block queues are
// rebuilt from those records.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	swept := make(map[uint64]bool, len(genState.SweptKeyIds))
	for _, id := range genState.SweptKeyIds {
		swept[id] = true
		if err := k.SweptKeys.Set(ctx, id); err != nil {
			return err
		}
		// Resume deleting its spent-ticket records, in case any remain.
		if err := k.Clearing.Set(ctx, id); err != nil {
			return err
		}
	}
	for _, elem := range genState.IssuerKeyList {
		if err := k.IssuerKey.Set(ctx, elem.Id, elem); err != nil {
			return err
		}
		// Keys not yet swept wait for their redemption deadline.
		if !swept[elem.Id] {
			if err := k.KeyExpiry.Set(ctx, collections.Join(elem.RedeemEnd, elem.Id)); err != nil {
				return err
			}
		}
	}
	for _, elem := range genState.SpentList {
		if err := k.Spent.Set(ctx, collections.Join(elem.KeyId, elem.TicketKey)); err != nil {
			return err
		}
	}
	for _, coin := range genState.ProberFund {
		if err := k.ProberFund.Set(ctx, coin.Denom, coin.Amount); err != nil {
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

	err = k.Spent.Walk(ctx, nil, func(key collections.Pair[uint64, []byte]) (bool, error) {
		genesis.SpentList = append(genesis.SpentList, types.SpentTicket{KeyId: key.K1(), TicketKey: key.K2()})
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	err = k.ProberFund.Walk(ctx, nil, func(denom string, amount math.Int) (bool, error) {
		if amount.IsPositive() {
			genesis.ProberFund = append(genesis.ProberFund, sdk.NewCoin(denom, amount))
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	err = k.SweptKeys.Walk(ctx, nil, func(id uint64) (bool, error) {
		genesis.SweptKeyIds = append(genesis.SweptKeyIds, id)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	return genesis, nil
}
