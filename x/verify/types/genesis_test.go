package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

func active(id uint64, owner string) types.Prober {
	return types.Prober{Id: id, Owner: owner, Bond: 1, Status: types.ProberActive}
}

func TestGenesisState_Validate(t *testing.T) {
	tests := []struct {
		desc     string
		genState *types.GenesisState
		valid    bool
	}{
		{
			desc:     "default is valid",
			genState: types.DefaultGenesis(),
			valid:    true,
		},
		{
			desc: "valid genesis state",
			genState: &types.GenesisState{
				Params:      types.DefaultParams(),
				ProberList:  []types.Prober{active(0, "a"), active(1, "b")},
				ProberCount: 2,
				NodeRecordList: []types.NodeRecord{
					{NodeId: 1}, {NodeId: 2},
				},
			},
			valid: true,
		},
		{
			desc: "all four statuses",
			genState: &types.GenesisState{
				Params: types.DefaultParams(),
				ProberList: []types.Prober{
					{Id: 0, Owner: "a", Status: types.ProberPending},
					{Id: 1, Owner: "b", Status: types.ProberActive},
					{Id: 2, Owner: "c", Status: types.ProberLeaving, UnbondingEnd: 10},
					{Id: 3, Owner: "d", Status: types.ProberUnbonding, UnbondingEnd: 10},
				},
				ProberCount: 4,
			},
			valid: true,
		},
		{
			desc: "duplicated prober",
			genState: &types.GenesisState{
				Params:      types.DefaultParams(),
				ProberList:  []types.Prober{active(0, "a"), active(0, "b")},
				ProberCount: 2,
			},
			valid: false,
		},
		{
			desc: "invalid prober count",
			genState: &types.GenesisState{
				Params:      types.DefaultParams(),
				ProberList:  []types.Prober{active(1, "a")},
				ProberCount: 0,
			},
			valid: false,
		},
		{
			desc: "two probers with one owner",
			genState: &types.GenesisState{
				Params:      types.DefaultParams(),
				ProberList:  []types.Prober{active(0, "a"), active(1, "a")},
				ProberCount: 2,
			},
			valid: false,
		},
		{
			desc: "prober without an owner",
			genState: &types.GenesisState{
				Params:      types.DefaultParams(),
				ProberList:  []types.Prober{active(0, "")},
				ProberCount: 1,
			},
			valid: false,
		},
		{
			desc: "unknown prober status",
			genState: &types.GenesisState{
				Params:      types.DefaultParams(),
				ProberList:  []types.Prober{{Id: 0, Owner: "a", Status: 9}},
				ProberCount: 1,
			},
			valid: false,
		},
		{
			desc: "active prober with an unbonding end",
			genState: &types.GenesisState{
				Params:      types.DefaultParams(),
				ProberList:  []types.Prober{{Id: 0, Owner: "a", Status: types.ProberActive, UnbondingEnd: 5}},
				ProberCount: 1,
			},
			valid: false,
		},
		{
			desc: "unbonding prober without an end",
			genState: &types.GenesisState{
				Params:      types.DefaultParams(),
				ProberList:  []types.Prober{{Id: 0, Owner: "a", Status: types.ProberUnbonding}},
				ProberCount: 1,
			},
			valid: false,
		},
		{
			desc: "duplicated node record",
			genState: &types.GenesisState{
				Params:         types.DefaultParams(),
				NodeRecordList: []types.NodeRecord{{NodeId: 1}, {NodeId: 1}},
			},
			valid: false,
		},
		{
			desc:     "zero params",
			genState: &types.GenesisState{},
			valid:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			err := tc.genState.Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestParamsValidate(t *testing.T) {
	require.NoError(t, types.DefaultParams().Validate())

	change := func(f func(*types.Params)) types.Params {
		p := types.DefaultParams()
		f(&p)
		return p
	}
	good := map[string]types.Params{
		"shortest round":        change(func(p *types.Params) { p.RoundBlocks = types.MinRoundBlocks }),
		"longest round":         change(func(p *types.Params) { p.RoundBlocks = types.MaxRoundBlocks }),
		"one prober per node":   change(func(p *types.Params) { p.ProbersPerNode = 1 }),
		"most probers":          change(func(p *types.Params) { p.ProbersPerNode = types.MaxProbersPerNode }),
		"smallest sample":       change(func(p *types.Params) { p.SampleBps = 1 }),
		"no unbonding period":   change(func(p *types.Params) { p.UnbondingSeconds = 0 }),
		"no tamper slash":       change(func(p *types.Params) { p.TamperSlashBps = 0 }),
		"full tamper slash":     change(func(p *types.Params) { p.TamperSlashBps = types.BpsDenominator }),
		"one fail before jail":  change(func(p *types.Params) { p.FailsBeforeJail = 1 }),
		"penalties switched on": change(func(p *types.Params) { p.ReportOnly = false }),
		"no unjail wait":        change(func(p *types.Params) { p.UnjailWaitSeconds = 0 }),
		"no report reward":      change(func(p *types.Params) { p.ReportReward = 0 }),
	}
	for name, p := range good {
		require.NoError(t, p.Validate(), name)
	}

	bad := map[string]types.Params{
		"round too short":         change(func(p *types.Params) { p.RoundBlocks = types.MinRoundBlocks - 1 }),
		"round too long":          change(func(p *types.Params) { p.RoundBlocks = types.MaxRoundBlocks + 1 }),
		"no probers per node":     change(func(p *types.Params) { p.ProbersPerNode = 0 }),
		"even probers per node":   change(func(p *types.Params) { p.ProbersPerNode = 4 }),
		"too many probers":        change(func(p *types.Params) { p.ProbersPerNode = types.MaxProbersPerNode + 2 }),
		"zero sample":             change(func(p *types.Params) { p.SampleBps = 0 }),
		"sample above 100%":       change(func(p *types.Params) { p.SampleBps = types.BpsDenominator + 1 }),
		"zero bond":               change(func(p *types.Params) { p.MinProberBond = 0 }),
		"unbonding over a year":   change(func(p *types.Params) { p.UnbondingSeconds = types.MaxUnbondingSeconds + 1 }),
		"zero fails before jail":  change(func(p *types.Params) { p.FailsBeforeJail = 0 }),
		"slash above 100%":        change(func(p *types.Params) { p.TamperSlashBps = types.BpsDenominator + 1 }),
		"unjail wait over a year": change(func(p *types.Params) { p.UnjailWaitSeconds = types.MaxUnbondingSeconds + 1 }),
		"everything zero":         {},
	}
	for name, p := range bad {
		require.ErrorIs(t, p.Validate(), types.ErrInvalidParams, name)
	}
}
