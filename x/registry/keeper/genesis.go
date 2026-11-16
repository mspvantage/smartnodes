package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	if genState.Params.Sponsor != "" {
		if _, err := k.addressCodec.StringToBytes(genState.Params.Sponsor); err != nil {
			return errorsmod.Wrapf(types.ErrInvalidParams, "sponsor address: %s", err.Error())
		}
	}

	var sponsored uint64
	for _, elem := range genState.NodeList {
		if elem.Sponsored > 0 {
			sponsored++
		}
		if err := k.Node.Set(ctx, elem.Id, elem); err != nil {
			return err
		}
		if err := k.NodeByPubkey.Set(ctx, elem.Pubkey, elem.Id); err != nil {
			return err
		}
	}

	if err := k.NodeSeq.Set(ctx, genState.NodeCount); err != nil {
		return err
	}
	// The count of sponsored nodes is rebuilt from the nodes themselves.
	if err := k.SponsoredCount.Set(ctx, sponsored); err != nil {
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
	err = k.Node.Walk(ctx, nil, func(key uint64, elem types.Node) (bool, error) {
		genesis.NodeList = append(genesis.NodeList, elem)
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	genesis.NodeCount, err = k.NodeSeq.Peek(ctx)
	if err != nil {
		return nil, err
	}

	return genesis, nil
}
