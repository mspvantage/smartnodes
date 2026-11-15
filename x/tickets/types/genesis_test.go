package types_test

import (
	"testing"

	"github.com/mspvantage/smartnodes/x/tickets/types"
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
			genState: &types.GenesisState{Params: types.DefaultParams(), IssuerKeyList: []types.IssuerKey{{Id: 0}, {Id: 1}}, IssuerKeyCount: 2, PurchaseList: []types.Purchase{{Id: 0}, {Id: 1}}, PurchaseCount: 2}, valid: true,
		}, {
			desc: "duplicated issuerKey",
			genState: &types.GenesisState{Params: types.DefaultParams(),
				IssuerKeyList: []types.IssuerKey{
					{
						Id: 0,
					},
					{
						Id: 0,
					},
				},
				PurchaseList: []types.Purchase{{Id: 0}, {Id: 1}}, PurchaseCount: 2,
			}, valid: false,
		}, {
			desc: "invalid issuerKey count",
			genState: &types.GenesisState{Params: types.DefaultParams(),
				IssuerKeyList: []types.IssuerKey{
					{
						Id: 1,
					},
				},
				IssuerKeyCount: 0,
				PurchaseList:   []types.Purchase{{Id: 0}, {Id: 1}}, PurchaseCount: 2,
			}, valid: false,
		}, {
			desc: "duplicated purchase",
			genState: &types.GenesisState{Params: types.DefaultParams(),
				PurchaseList: []types.Purchase{
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
			desc: "invalid purchase count",
			genState: &types.GenesisState{Params: types.DefaultParams(),
				PurchaseList: []types.Purchase{
					{
						Id: 1,
					},
				},
				PurchaseCount: 0,
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
