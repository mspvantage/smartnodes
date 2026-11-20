package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

func TestAimedBps(t *testing.T) {
	p := types.DefaultParams()
	p.SampleBps = 1_000 // 10% base
	steady := types.NodeRecord{Passes: 50}
	for name, tc := range map[string]struct {
		rec  *types.NodeRecord
		want uint64
	}{
		"never seen":           {nil, 4_000},
		"new":                  {&types.NodeRecord{Passes: 2}, 4_000},
		"ordinary":             {&types.NodeRecord{Passes: 10}, 1_000},
		"failing":              {&types.NodeRecord{Passes: 50, ConsecutiveFails: 1}, 4_000},
		"just unjailed":        {&types.NodeRecord{Passes: 50, UnjailedRound: 90}, 4_000},
		"unjailed long ago":    {&types.NodeRecord{Passes: 50, UnjailedRound: 10}, 500},
		"flagged":              {&types.NodeRecord{Passes: 50, FlaggedFrom: 95, FlaggedUntil: 110}, 3_000},
		"flag not yet started": {&types.NodeRecord{Passes: 50, FlaggedFrom: 101, FlaggedUntil: 110}, 500},
		"flag over":            {&types.NodeRecord{Passes: 50, FlaggedFrom: 50, FlaggedUntil: 60}, 500},
		"steady":               {&steady, 500},
	} {
		require.Equal(t, tc.want, types.AimedBps(p, tc.rec, 100), name)
	}

	// Never above every round, never zero.
	p.SampleBps = 5_000
	require.Equal(t, uint64(types.BpsDenominator), types.AimedBps(p, nil, 1))
	p.SampleBps = 1
	require.Equal(t, uint64(1), types.AimedBps(p, &steady, 1))
}

func TestAimParams(t *testing.T) {
	p := types.DefaultParams()
	require.NoError(t, p.Validate())
	for name, change := range map[string]func(*types.Params){
		"no boost":     func(p *types.Params) { p.AimBoostFactor = 0 },
		"huge flag":    func(p *types.Params) { p.AimFlagFactor = types.MaxAimFactor + 1 },
		"no window":    func(p *types.Params) { p.AimWindowRounds = 0 },
		"steady zero":  func(p *types.Params) { p.AimSteadyPct = 0 },
		"steady above": func(p *types.Params) { p.AimSteadyPct = 101 },
	} {
		q := types.DefaultParams()
		change(&q)
		require.ErrorIs(t, q.Validate(), types.ErrInvalidParams, name)
	}
}
