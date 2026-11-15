package keeper_test

import (
	"testing"

	"github.com/mspvantage/smartnodes/x/registry/types"

	"github.com/stretchr/testify/require"
)

func TestGenesis(t *testing.T) {
	genesisState := types.GenesisState{
		Params:    types.DefaultParams(),
		NodeList:  []types.Node{{Id: 0}, {Id: 1}},
		NodeCount: 2,
	}
	f := initFixture(t)
	err := f.keeper.InitGenesis(f.ctx, genesisState)
	require.NoError(t, err)
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.EqualExportedValues(t, genesisState.Params, got.Params)
	require.EqualExportedValues(t, genesisState.NodeList, got.NodeList)
	require.Equal(t, genesisState.NodeCount, got.NodeCount)

}
