package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

func TestGenesis(t *testing.T) {
	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
		ProberList: []types.Prober{
			{Id: 0, Owner: "a", Bond: 1, Status: types.ProberActive},
			{Id: 1, Owner: "b", Bond: 2, Status: types.ProberPending},
		},
		ProberCount:    2,
		NodeRecordList: []types.NodeRecord{{NodeId: 7, Verified: true, LastRound: 3, LastPassRound: 3, Passes: 2}},
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
	require.EqualExportedValues(t, genesisState.NodeRecordList, got.NodeRecordList)
}

// Importing genesis rebuilds everything that is not stored in it: the owner
// index, the active list and the two waiting lists.
func TestGenesisRebuildsIndexes(t *testing.T) {
	// Build real state, export it, and import it into a fresh chain.
	w := newWorld(t)
	w.play(t, 1, [3]uint64{pass, pass, fail})
	late := w.newProber(t, 0x61) // pending
	_, err := w.ms.DeregisterProber(w.ctx, &types.MsgDeregisterProber{Owner: w.p[1].str})
	require.NoError(t, err) // leaving
	exported, err := w.keeper.ExportGenesis(w.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.ProberList, 4)
	require.Len(t, exported.NodeRecordList, 1)

	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *exported))
	f.requireActiveConsistent(t)

	count, err := f.keeper.ActiveCount.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), count)

	for _, p := range []*tester{w.p[0], w.p[1], w.p[2], late} {
		id, err := f.keeper.ProberByOwner.Get(f.ctx, p.str)
		require.NoError(t, err)
		require.Equal(t, p.id, id)
	}
	joining, err := f.keeper.PendingJoin.Has(f.ctx, late.id)
	require.NoError(t, err)
	require.True(t, joining)
	leaving, err := f.keeper.PendingLeave.Has(f.ctx, w.p[1].id)
	require.NoError(t, err)
	require.True(t, leaving)

	// Scores and node records survive.
	require.Equal(t, uint64(1), f.prober(t, w.p[0].id).Accepted)
	require.Equal(t, uint64(1), f.prober(t, w.p[2].id).Outliers)
	rec, err := f.keeper.NodeRecord.Get(f.ctx, 1)
	require.NoError(t, err)
	require.True(t, rec.Verified)

	// The restarted chain begins a fresh round and applies the waiting
	// changes when it starts.
	require.Equal(t, uint64(0), f.round(t))
	f.begin(t)
	require.Equal(t, uint64(1), f.round(t))
	f.requireActiveConsistent(t)
	require.Equal(t, types.ProberActive, f.prober(t, late.id).Status)
	require.Equal(t, types.ProberUnbonding, f.prober(t, w.p[1].id).Status)

}
