package keeper_test

import (
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/verify/keeper"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

// How often a node is probed is fixed when a round starts; a flag counts
// from the next round, raises the rate, and costs the flagger a ticket.
func TestFlagRaisesProbingFromNextRound(t *testing.T) {
	f := initFixture(t)
	p := f.newProber(t, 1)
	f.setParams(t, func(p *types.Params) { p.SampleBps = 1; p.AimFlagFactor = 20; p.AimWindowRounds = 3 })
	f.addNode(1, registrytypes.StatusActive)
	// An established node, probed at the steady rate.
	require.NoError(t, f.keeper.NodeRecord.Set(f.ctx, 1, types.NodeRecord{NodeId: 1, Passes: 100}))
	f.begin(t)
	f.nextRound(t)

	aim := func() uint64 {
		t.Helper()
		bps, err := f.keeper.RoundAim.Get(f.ctx, collections.Join(f.round(t), uint64(1)))
		require.NoError(t, err)
		return bps
	}
	require.Equal(t, uint64(1), aim(), "steady, but never zero")

	_, err := f.ms.FlagNode(f.scratch(), &types.MsgFlagNode{Signer: p.str, NodeId: 1, KeyId: 1, Ticket: "bad"})
	require.Error(t, err)
	res, err := f.ms.FlagNode(f.ctx, &types.MsgFlagNode{Signer: p.str, NodeId: 1, KeyId: 1, Ticket: "t1"})
	require.NoError(t, err)
	require.Equal(t, f.round(t)+3, res.Until)
	// The same ticket flags once.
	_, err = f.ms.FlagNode(f.scratch(), &types.MsgFlagNode{Signer: p.str, NodeId: 1, KeyId: 1, Ticket: "t1"})
	require.Error(t, err)
	require.Equal(t, []string{"t1"}, f.tickets.flagged[1])

	// This round is unchanged; the next ones are raised, then it ends.
	require.Equal(t, uint64(1), aim())
	f.nextRound(t)
	require.Equal(t, uint64(20), aim())
	f.nextRound(t)
	f.nextRound(t)
	require.Equal(t, uint64(20), aim())
	f.nextRound(t)
	require.Equal(t, uint64(1), aim())
	rec, _ := f.keeper.NodeRecord.Get(f.ctx, 1)
	require.Equal(t, uint64(1), rec.Flags)
}

// Probing a node more often must not change what it earns over time.
func TestPassWeightEvensOutProbeRate(t *testing.T) {
	require.Equal(t, uint64(1000), keeper.PassWeight(1000, 2500, 2500))
	require.Equal(t, uint64(250), keeper.PassWeight(1000, 2500, 10_000), "probed 4x as often: a quarter per pass")
	require.Equal(t, uint64(2000), keeper.PassWeight(1000, 2500, 1250), "probed half as often: double per pass")
	require.Equal(t, uint64(1000), keeper.PassWeight(1000, 2500, 0))
}
