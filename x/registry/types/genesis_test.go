package types_test

import (
	"testing"

	"github.com/mspvantage/smartnodes/x/registry/types"
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
			desc:     "valid genesis state",
			genState: &types.GenesisState{NodeList: []types.Node{{Id: 0}, {Id: 1}}, NodeCount: 2}, valid: true,
		}, {
			desc: "duplicated node",
			genState: &types.GenesisState{
				NodeList: []types.Node{
					{
						Id: 0,
					},
					{
						Id: 0,
					},
				},
			},
			valid: false,
		}, {
			desc: "invalid node count",
			genState: &types.GenesisState{
				NodeList: []types.Node{
					{
						Id: 1,
					},
				},
				NodeCount: 0,
			},
			valid: false,
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
