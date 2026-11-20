package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

// One query per prober gives exactly what asking about each node gives.
func TestProberAssignmentsMatchPerNode(t *testing.T) {
	f := initFixture(t)
	var probers []*tester
	for i := byte(1); i <= 5; i++ {
		probers = append(probers, f.newProber(t, i))
	}
	f.setParams(t, func(p *types.Params) { p.SampleBps = 5000 })
	for id := uint64(1); id <= 30; id++ {
		f.addNode(id, registrytypes.StatusActive)
	}
	// One node leaving and one jailed are never assigned.
	f.addNode(31, registrytypes.StatusUnbonding)
	f.addNode(32, registrytypes.StatusActive)
	f.begin(t)
	f.nextRound(t)
	require.NoError(t, f.keeper.NodeRecord.Set(f.ctx, 32, types.NodeRecord{NodeId: 32, Jailed: true}))

	want := map[uint64][]uint64{}
	for id := uint64(1); id <= 30; id++ {
		res, err := f.qs.Assignment(f.ctx, &types.QueryAssignmentRequest{NodeId: id})
		require.NoError(t, err)
		for _, p := range res.ProberIds {
			want[p] = append(want[p], id)
		}
	}
	total := 0
	for _, p := range probers {
		res, err := f.qs.ProberAssignments(f.ctx, &types.QueryProberAssignmentsRequest{ProberId: p.id})
		require.NoError(t, err)
		require.Equal(t, f.round(t), res.Round)
		require.Equal(t, want[p.id], res.NodeIds, "prober %d", p.id)
		require.NotContains(t, res.NodeIds, uint64(31))
		require.NotContains(t, res.NodeIds, uint64(32))
		total += len(res.NodeIds)
	}
	require.Positive(t, total)

	// A prober that does not exist is assigned nothing.
	res, err := f.qs.ProberAssignments(f.ctx, &types.QueryProberAssignmentsRequest{ProberId: 999})
	require.NoError(t, err)
	require.Empty(t, res.NodeIds)
}

func TestMinSpeedParams(t *testing.T) {
	p := types.DefaultParams()
	require.Equal(t, types.DefaultMinEntryMbps, p.MinEntryMbps)
	require.NoError(t, p.Validate())
	p.MinExitMbps = types.MaxMinMbps + 1
	require.ErrorIs(t, p.Validate(), types.ErrInvalidParams)
	p.MinExitMbps = 0
	require.NoError(t, p.Validate(), "zero means no minimum")
}
