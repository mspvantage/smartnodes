package keeper_test

import (
	"testing"

	"github.com/mspvantage/smartnodes/x/registry/keeper"
	"github.com/mspvantage/smartnodes/x/registry/types"

	"github.com/stretchr/testify/require"
)

func TestGenesis(t *testing.T) {
	f := initFixture(t)
	_, op := f.account(t, 1)

	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
		NodeList: []types.Node{
			{Id: 0, Operator: op, Endpoint: "a.example.com:51820", Pubkey: pubkey(1), Tier: types.TierEntry, Bond: bond(entryBond), Status: types.StatusActive},
			{Id: 1, Operator: op, Endpoint: "b.example.com:51820", Pubkey: pubkey(2), Tier: types.TierExit, Bond: bond(exitBond), Status: types.StatusUnbonding, UnbondingEnd: 1_900_000_000},
		},
		NodeCount: 2,
	}
	require.NoError(t, genesisState.Validate())

	err := f.keeper.InitGenesis(f.ctx, genesisState)
	require.NoError(t, err)
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.EqualExportedValues(t, genesisState.Params, got.Params)
	require.EqualExportedValues(t, genesisState.NodeList, got.NodeList)
	require.Equal(t, genesisState.NodeCount, got.NodeCount)
}

// TestGenesisRebuildsPubkeyIndex checks that a public key imported through
// genesis is protected against a second registration.
func TestGenesisRebuildsPubkeyIndex(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, 10_000_000_000)

	require.NoError(t, f.keeper.InitGenesis(f.ctx, types.GenesisState{
		Params: types.DefaultParams(),
		NodeList: []types.Node{
			{Id: 0, Operator: op, Endpoint: "a.example.com:51820", Pubkey: pubkey(1), Tier: types.TierEntry, Bond: bond(entryBond), Status: types.StatusActive},
		},
		NodeCount: 1,
	}))

	_, err := ms.RegisterNode(f.ctx, &types.MsgRegisterNode{
		Operator: op, Endpoint: "c.example.com:51820", Pubkey: pubkey(1), Tier: types.TierEntry, Bond: bond(entryBond),
	})
	require.ErrorIs(t, err, types.ErrPubkeyTaken)

	// a new key registers and takes the next id after the imported ones
	id := register(t, f, ms, op, 2, types.TierEntry, entryBond)
	require.Equal(t, uint64(1), id)
}
