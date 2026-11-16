package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"

	"github.com/mspvantage/smartnodes/x/rewards/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	// Every non-voting address must be readable. A typing mistake here would
	// otherwise leave tokens voting that were meant not to, with no warning.
	for _, addr := range genState.Params.NonVotingAddresses {
		if _, err := k.addressCodec.StringToBytes(addr); err != nil {
			return errorsmod.Wrapf(types.ErrInvalidParams, "non-voting address %q: %s", addr, err.Error())
		}
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

	return genesis, nil
}
