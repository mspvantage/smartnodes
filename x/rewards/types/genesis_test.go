package types_test

import (
	"testing"

	"github.com/mspvantage/smartnodes/x/rewards/types"
	"github.com/stretchr/testify/require"
)

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
			desc:     "explicit default params",
			genState: &types.GenesisState{Params: types.DefaultParams()},
			valid:    true,
		},
		{
			// A commission ceiling of zero would stop any validator from
			// being created, so empty params are refused.
			desc:     "zero params are rejected",
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
