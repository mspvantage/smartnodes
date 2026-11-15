package types_test

import (
	"encoding/base64"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

func testPubkey(seed byte) string {
	bz := make([]byte, types.PubkeyLength)
	for i := range bz {
		bz[i] = seed
	}
	return base64.StdEncoding.EncodeToString(bz)
}

func validNode(id uint64, key byte) types.Node {
	return types.Node{
		Id:       id,
		Operator: "operator-address",
		Endpoint: "node.example.com:51820",
		Pubkey:   testPubkey(key),
		Tier:     types.TierEntry,
		Bond:     sdk.NewCoin("usnode", math.NewInt(1_000_000_000)),
		Status:   types.StatusActive,
	}
}

func TestGenesisState_Validate(t *testing.T) {
	with := func(n types.Node, mutate func(*types.Node)) types.Node {
		mutate(&n)
		return n
	}

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
				Params:    types.DefaultParams(),
				NodeList:  []types.Node{validNode(0, 1), validNode(1, 2)},
				NodeCount: 2,
			},
			valid: true,
		},
		{
			desc: "valid unbonding node",
			genState: &types.GenesisState{
				Params: types.DefaultParams(),
				NodeList: []types.Node{with(validNode(0, 1), func(n *types.Node) {
					n.Status = types.StatusUnbonding
					n.UnbondingEnd = 1_900_000_000
				})},
				NodeCount: 1,
			},
			valid: true,
		},
		{
			desc: "duplicated node",
			genState: &types.GenesisState{
				Params:    types.DefaultParams(),
				NodeList:  []types.Node{validNode(0, 1), validNode(0, 2)},
				NodeCount: 2,
			},
			valid: false,
		},
		{
			desc: "invalid node count",
			genState: &types.GenesisState{
				Params:    types.DefaultParams(),
				NodeList:  []types.Node{validNode(1, 1)},
				NodeCount: 0,
			},
			valid: false,
		},
		{
			desc: "duplicated public key",
			genState: &types.GenesisState{
				Params:    types.DefaultParams(),
				NodeList:  []types.Node{validNode(0, 1), validNode(1, 1)},
				NodeCount: 2,
			},
			valid: false,
		},
		{
			desc: "empty node record",
			genState: &types.GenesisState{
				Params:    types.DefaultParams(),
				NodeList:  []types.Node{{Id: 0}},
				NodeCount: 1,
			},
			valid: false,
		},
		{
			desc: "unknown tier",
			genState: &types.GenesisState{
				Params:    types.DefaultParams(),
				NodeList:  []types.Node{with(validNode(0, 1), func(n *types.Node) { n.Tier = 7 })},
				NodeCount: 1,
			},
			valid: false,
		},
		{
			desc: "unknown status",
			genState: &types.GenesisState{
				Params:    types.DefaultParams(),
				NodeList:  []types.Node{with(validNode(0, 1), func(n *types.Node) { n.Status = 0 })},
				NodeCount: 1,
			},
			valid: false,
		},
		{
			desc: "unbonding without an end time",
			genState: &types.GenesisState{
				Params:    types.DefaultParams(),
				NodeList:  []types.Node{with(validNode(0, 1), func(n *types.Node) { n.Status = types.StatusUnbonding })},
				NodeCount: 1,
			},
			valid: false,
		},
		{
			desc: "active with an end time",
			genState: &types.GenesisState{
				Params:    types.DefaultParams(),
				NodeList:  []types.Node{with(validNode(0, 1), func(n *types.Node) { n.UnbondingEnd = 5 })},
				NodeCount: 1,
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
