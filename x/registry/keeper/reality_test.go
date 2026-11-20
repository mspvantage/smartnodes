package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/registry/keeper"
	"github.com/mspvantage/smartnodes/x/registry/types"
)

const (
	testRealityKey  = "Kuwv0McuvHt93baQG_nFirbjazMXkDlCt1302dy31GU"
	testRealityUUID = "d5c8eb88-00c3-463e-a6ec-a0f55502cd87"
)

func reality(port uint32) types.MsgSetReality {
	return types.MsgSetReality{Port: port, PublicKey: testRealityKey, ShortId: "0123456789abcdef", ServerName: "www.example.com", Uuid: testRealityUUID}
}

func TestSetReality(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, 20_000_000_000)
	_, other := f.account(t, 2)
	entry := register(t, f, ms, op, 1, types.TierEntry, entryBond)
	exit := register(t, f, ms, op, 2, types.TierExit, exitBond)

	m := reality(443)
	m.Operator, m.Id = op, entry
	_, err := ms.SetReality(f.ctx, &m)
	require.NoError(t, err)
	node, err := f.keeper.GetNode(f.ctx, entry)
	require.NoError(t, err)
	require.NotNil(t, node.Reality)
	require.Equal(t, uint32(443), node.Reality.Port)
	require.Equal(t, testRealityKey, node.Reality.PublicKey)
	require.NoError(t, node.ValidateBasic())

	// The same again changes nothing.
	_, err = ms.SetReality(f.ctx, &m)
	require.ErrorIs(t, err, types.ErrNothingToUpdate)

	// Only the operator, and only for an entry node.
	bad := m
	bad.Operator = other
	_, err = ms.SetReality(f.ctx, &bad)
	require.ErrorIs(t, err, types.ErrNotOperator)
	bad = m
	bad.Id = exit
	_, err = ms.SetReality(f.ctx, &bad)
	require.ErrorIs(t, err, types.ErrInvalidReality)

	// Bad values.
	for name, change := range map[string]func(*types.MsgSetReality){
		"short key":     func(x *types.MsgSetReality) { x.PublicKey = "abc" },
		"odd short id":  func(x *types.MsgSetReality) { x.ShortId = "abc" },
		"upper hex":     func(x *types.MsgSetReality) { x.ShortId = "ABCD" },
		"bad name":      func(x *types.MsgSetReality) { x.ServerName = "not a name" },
		"bad uuid":      func(x *types.MsgSetReality) { x.Uuid = "x" },
		"too big port":  func(x *types.MsgSetReality) { x.Port = 70000 },
		"half a remove": func(x *types.MsgSetReality) { x.Port = 0 },
	} {
		b := m
		b.Port = 8443
		change(&b)
		_, err := ms.SetReality(f.ctx, &b)
		require.ErrorIs(t, err, types.ErrInvalidReality, name)
	}

	// Removing it.
	_, err = ms.SetReality(f.ctx, &types.MsgSetReality{Operator: op, Id: entry})
	require.NoError(t, err)
	node, _ = f.keeper.GetNode(f.ctx, entry)
	require.Nil(t, node.Reality)
	_, err = ms.SetReality(f.ctx, &types.MsgSetReality{Operator: op, Id: entry})
	require.ErrorIs(t, err, types.ErrNothingToUpdate)
}
