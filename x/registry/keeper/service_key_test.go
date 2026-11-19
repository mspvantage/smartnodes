package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/registry/keeper"
	"github.com/mspvantage/smartnodes/x/registry/types"
)

func TestSetServiceKey(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, 20_000_000_000)
	_, other := f.account(t, 2)
	_, service := f.account(t, 3)
	id := register(t, f, ms, op, 1, types.TierEntry, entryBond)

	_, err := ms.SetServiceKey(f.ctx, &types.MsgSetServiceKey{Operator: op, Id: id, ServiceKey: service})
	require.NoError(t, err)
	node, err := f.keeper.GetNode(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, service, node.ServiceKey)
	require.NoError(t, node.ValidateBasic())

	// The same key again changes nothing.
	_, err = ms.SetServiceKey(f.ctx, &types.MsgSetServiceKey{Operator: op, Id: id, ServiceKey: service})
	require.ErrorIs(t, err, types.ErrNothingToUpdate)

	// Only the operator may set it; not another account, not the service key.
	_, err = ms.SetServiceKey(f.ctx, &types.MsgSetServiceKey{Operator: other, Id: id, ServiceKey: other})
	require.ErrorIs(t, err, types.ErrNotOperator)
	_, err = ms.SetServiceKey(f.ctx, &types.MsgSetServiceKey{Operator: service, Id: id, ServiceKey: ""})
	require.ErrorIs(t, err, types.ErrNotOperator)

	// Not the operator's own address, and not garbage.
	_, err = ms.SetServiceKey(f.ctx, &types.MsgSetServiceKey{Operator: op, Id: id, ServiceKey: op})
	require.ErrorIs(t, err, types.ErrInvalidServiceKey)
	_, err = ms.SetServiceKey(f.ctx, &types.MsgSetServiceKey{Operator: op, Id: id, ServiceKey: "smart1notanaddress"})
	require.ErrorIs(t, err, types.ErrInvalidServiceKey)

	// Clearing it.
	_, err = ms.SetServiceKey(f.ctx, &types.MsgSetServiceKey{Operator: op, Id: id, ServiceKey: ""})
	require.NoError(t, err)
	node, _ = f.keeper.GetNode(f.ctx, id)
	require.Equal(t, "", node.ServiceKey)

	// A node that has left cannot be given one.
	_, err = ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: op, Id: id})
	require.NoError(t, err)
	_, err = ms.SetServiceKey(f.ctx, &types.MsgSetServiceKey{Operator: op, Id: id, ServiceKey: service})
	require.ErrorIs(t, err, types.ErrNodeNotActive)
}

func TestNodeServiceKeyValidation(t *testing.T) {
	n := types.Node{Operator: "smart1op", Tier: types.TierEntry, Endpoint: "node.example.com:51820",
		Pubkey: pubkey(1), Bond: bond(entryBond), Status: types.StatusActive}
	require.NoError(t, n.ValidateBasic())
	n.ServiceKey = "smart1other"
	require.NoError(t, n.ValidateBasic())
	n.ServiceKey = n.Operator
	require.ErrorIs(t, n.ValidateBasic(), types.ErrInvalidServiceKey)
}
