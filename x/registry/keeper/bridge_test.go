package keeper_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/registry/keeper"
	"github.com/mspvantage/smartnodes/x/registry/types"
)

var (
	fingerprint  = strings.Repeat("ab", 32)
	fingerprint2 = strings.Repeat("cd", 32)
)

func TestBridges(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, 20_000_000_000)
	_, other := f.account(t, 2)

	res, err := ms.RegisterBridge(f.ctx, &types.MsgRegisterBridge{Operator: op, Commitment: fingerprint, Bond: bond(entryBond)})
	require.NoError(t, err)
	n, err := f.keeper.GetNode(f.ctx, res.Id)
	require.NoError(t, err)
	// Nothing that finds or confirms the bridge is on the chain.
	require.Equal(t, "", n.Endpoint)
	require.Equal(t, "", n.Pubkey)
	require.Nil(t, n.Reality)
	require.Equal(t, types.TierEntry, n.Tier)
	require.Equal(t, fingerprint, n.Commitment)
	require.NoError(t, n.ValidateBasic())

	// Two bridges: there is no key to clash on.
	_, err = ms.RegisterBridge(f.ctx, &types.MsgRegisterBridge{Operator: op, Commitment: fingerprint2, Bond: bond(entryBond)})
	require.NoError(t, err)

	// Bad fingerprints and bonds.
	for _, c := range []string{"", "AB", strings.Repeat("AB", 32), strings.Repeat("ab", 31)} {
		_, err := ms.RegisterBridge(f.ctx, &types.MsgRegisterBridge{Operator: op, Commitment: c, Bond: bond(entryBond)})
		require.ErrorIs(t, err, types.ErrInvalidCommitment, c)
	}
	_, err = ms.RegisterBridge(f.ctx, &types.MsgRegisterBridge{Operator: op, Commitment: fingerprint, Bond: bond(entryBond - 1)})
	require.ErrorIs(t, err, types.ErrBondTooLow)

	// A bridge never gets a public address or disguise.
	_, err = ms.UpdateNode(f.ctx, &types.MsgUpdateNode{Operator: op, Id: res.Id, Endpoint: "bridge.example.com:51820"})
	require.ErrorIs(t, err, types.ErrIsBridge)
	m := reality(443)
	m.Operator, m.Id = op, res.Id
	_, err = ms.SetReality(f.ctx, &m)
	require.ErrorIs(t, err, types.ErrIsBridge)

	// Moving: only the operator, only a bridge, only a change.
	_, err = ms.UpdateBridge(f.ctx, &types.MsgUpdateBridge{Operator: other, Id: res.Id, Commitment: fingerprint2})
	require.ErrorIs(t, err, types.ErrNotOperator)
	_, err = ms.UpdateBridge(f.ctx, &types.MsgUpdateBridge{Operator: op, Id: res.Id, Commitment: fingerprint})
	require.ErrorIs(t, err, types.ErrNothingToUpdate)
	_, err = ms.UpdateBridge(f.ctx, &types.MsgUpdateBridge{Operator: op, Id: res.Id, Commitment: fingerprint2})
	require.NoError(t, err)
	n, _ = f.keeper.GetNode(f.ctx, res.Id)
	require.Equal(t, fingerprint2, n.Commitment)
	listed := register(t, f, ms, op, 9, types.TierEntry, entryBond)
	_, err = ms.UpdateBridge(f.ctx, &types.MsgUpdateBridge{Operator: op, Id: listed, Commitment: fingerprint})
	require.ErrorIs(t, err, types.ErrIsBridge)

	// A bridge leaves like any node.
	_, err = ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: op, Id: res.Id})
	require.NoError(t, err)
}

func TestBridgeValidation(t *testing.T) {
	n := types.Node{Operator: "smart1op", Tier: types.TierEntry, Bond: bond(entryBond), Status: types.StatusActive, Commitment: fingerprint}
	require.NoError(t, n.ValidateBasic())
	for name, change := range map[string]func(*types.Node){
		"with an endpoint": func(x *types.Node) { x.Endpoint = "a.example.com:1" },
		"with a pubkey":    func(x *types.Node) { x.Pubkey = pubkey(1) },
		"an exit":          func(x *types.Node) { x.Tier = types.TierExit },
	} {
		b := n
		change(&b)
		require.ErrorIs(t, b.ValidateBasic(), types.ErrIsBridge, name)
	}
}
