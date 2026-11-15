package keeper_test

import (
	"testing"

	"github.com/mspvantage/smartnodes/x/verify/types"

	"github.com/stretchr/testify/require"
)

func TestGenesis(t *testing.T) {
	genesisState := types.GenesisState{
		Params:      types.DefaultParams(),
		ProberList:  []types.Prober{{Id: 0}, {Id: 1}},
		ProberCount: 2,
	}
	f := initFixture(t)
	err := f.keeper.InitGenesis(f.ctx, genesisState)
	require.NoError(t, err)
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.EqualExportedValues(t, genesisState.Params, got.Params)
	require.EqualExportedValues(t, genesisState.ProberList, got.ProberList)
	require.Equal(t, genesisState.ProberCount, got.ProberCount)

}
