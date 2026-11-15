package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/registry/keeper"
	"github.com/mspvantage/smartnodes/x/registry/types"
)

const (
	entryBond = uint64(1_000_000_000) // default minimum for entry nodes
	exitBond  = uint64(5_000_000_000) // default minimum for exit nodes
)

// register is a helper that registers an entry node and fails the test if it
// does not succeed.
func register(t *testing.T, f *fixture, ms types.MsgServer, operator string, key byte, tier, amount uint64) uint64 {
	t.Helper()
	res, err := ms.RegisterNode(f.ctx, &types.MsgRegisterNode{
		Operator: operator,
		Endpoint: "node.example.com:51820",
		Pubkey:   pubkey(key),
		Tier:     tier,
		Bond:     bond(amount),
	})
	require.NoError(t, err)
	return res.Id
}

func TestRegisterNode(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, 20_000_000_000)

	id0 := register(t, f, ms, op, 1, types.TierEntry, entryBond)
	id1 := register(t, f, ms, op, 2, types.TierExit, exitBond+7)
	require.Equal(t, uint64(0), id0)
	require.Equal(t, uint64(1), id1)

	node, err := f.keeper.GetNode(f.ctx, id1)
	require.NoError(t, err)
	require.Equal(t, op, node.Operator)
	require.Equal(t, types.TierExit, node.Tier)
	require.Equal(t, types.StatusActive, node.Status)
	require.Equal(t, int64(0), node.UnbondingEnd)
	requireCoin(t, bond(exitBond+7), node.Bond)
	require.Equal(t, pubkey(2), node.Pubkey)

	// the bond left the operator and sits in the module account
	requireInt(t, math.NewIntFromUint64(20_000_000_000-entryBond-exitBond-7), f.balance(addr))
	requireInt(t, math.NewIntFromUint64(entryBond+exitBond+7), f.moduleBalance())
	f.requireInvariant(t)
}

func TestRegisterNodeRejects(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, 20_000_000_000)
	_, poorOp := f.account(t, 9) // has no funds

	// one node already exists, to test the duplicate key rule
	register(t, f, ms, op, 1, types.TierEntry, entryBond)

	valid := func() *types.MsgRegisterNode {
		return &types.MsgRegisterNode{
			Operator: op,
			Endpoint: "198.51.100.7:51820",
			Pubkey:   pubkey(50),
			Tier:     types.TierEntry,
			Bond:     bond(entryBond),
		}
	}

	tests := []struct {
		name   string
		mutate func(m *types.MsgRegisterNode)
		err    error
	}{
		{"unknown tier", func(m *types.MsgRegisterNode) { m.Tier = 3 }, types.ErrInvalidTier},
		{"tier zero", func(m *types.MsgRegisterNode) { m.Tier = 0 }, types.ErrInvalidTier},
		{"endpoint without port", func(m *types.MsgRegisterNode) { m.Endpoint = "node.example.com" }, types.ErrInvalidEndpoint},
		{"endpoint port zero", func(m *types.MsgRegisterNode) { m.Endpoint = "node.example.com:0" }, types.ErrInvalidEndpoint},
		{"empty endpoint", func(m *types.MsgRegisterNode) { m.Endpoint = "" }, types.ErrInvalidEndpoint},
		{"pubkey not base64", func(m *types.MsgRegisterNode) { m.Pubkey = "not a key" }, types.ErrInvalidPubkey},
		{"pubkey wrong length", func(m *types.MsgRegisterNode) { m.Pubkey = "AAAA" }, types.ErrInvalidPubkey},
		{"pubkey already registered", func(m *types.MsgRegisterNode) { m.Pubkey = pubkey(1) }, types.ErrPubkeyTaken},
		{"wrong denom", func(m *types.MsgRegisterNode) { m.Bond = sdk.NewInt64Coin("token", 9_000_000_000) }, types.ErrInvalidBond},
		{"zero bond", func(m *types.MsgRegisterNode) { m.Bond = bond(0) }, types.ErrInvalidBond},
		{"entry bond one unit short", func(m *types.MsgRegisterNode) { m.Bond = bond(entryBond - 1) }, types.ErrBondTooLow},
		{"exit tier with entry-sized bond", func(m *types.MsgRegisterNode) { m.Tier = types.TierExit }, types.ErrBondTooLow},
		{"operator cannot pay", func(m *types.MsgRegisterNode) { m.Operator = poorOp }, sdkerrors.ErrInsufficientFunds},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := valid()
			tc.mutate(msg)
			_, err := ms.RegisterNode(f.ctx, msg)
			require.ErrorIs(t, err, tc.err)
		})
	}

	t.Run("invalid operator address", func(t *testing.T) {
		msg := valid()
		msg.Operator = "not-an-address"
		_, err := ms.RegisterNode(f.ctx, msg)
		require.Error(t, err)
	})

	// none of the rejected attempts changed anything
	requireInt(t, math.NewIntFromUint64(entryBond), f.moduleBalance())
	seq, err := f.keeper.NodeSeq.Peek(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), seq)
	f.requireInvariant(t)

	// and the untouched valid message still works
	_, err = ms.RegisterNode(f.ctx, valid())
	require.NoError(t, err)
	f.requireInvariant(t)
}

func TestUpdateNode(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, 20_000_000_000)
	_, stranger := f.account(t, 2)

	id := register(t, f, ms, op, 1, types.TierEntry, entryBond)
	other := register(t, f, ms, op, 2, types.TierEntry, entryBond)

	t.Run("change endpoint only", func(t *testing.T) {
		_, err := ms.UpdateNode(f.ctx, &types.MsgUpdateNode{Operator: op, Id: id, Endpoint: "203.0.113.5:443"})
		require.NoError(t, err)
		node, err := f.keeper.GetNode(f.ctx, id)
		require.NoError(t, err)
		require.Equal(t, "203.0.113.5:443", node.Endpoint)
		require.Equal(t, pubkey(1), node.Pubkey)
	})

	t.Run("change public key frees the old one", func(t *testing.T) {
		_, err := ms.UpdateNode(f.ctx, &types.MsgUpdateNode{Operator: op, Id: id, Pubkey: pubkey(3)})
		require.NoError(t, err)
		node, err := f.keeper.GetNode(f.ctx, id)
		require.NoError(t, err)
		require.Equal(t, pubkey(3), node.Pubkey)

		oldTaken, err := f.keeper.NodeByPubkey.Has(f.ctx, pubkey(1))
		require.NoError(t, err)
		require.False(t, oldTaken)
		indexed, err := f.keeper.NodeByPubkey.Get(f.ctx, pubkey(3))
		require.NoError(t, err)
		require.Equal(t, id, indexed)
	})

	t.Run("rejects another node's public key", func(t *testing.T) {
		_, err := ms.UpdateNode(f.ctx, &types.MsgUpdateNode{Operator: op, Id: id, Pubkey: pubkey(2)})
		require.ErrorIs(t, err, types.ErrPubkeyTaken)
	})

	t.Run("rejects a stranger", func(t *testing.T) {
		_, err := ms.UpdateNode(f.ctx, &types.MsgUpdateNode{Operator: stranger, Id: id, Endpoint: "203.0.113.9:443"})
		require.ErrorIs(t, err, types.ErrNotOperator)
	})

	t.Run("rejects an invalid endpoint", func(t *testing.T) {
		_, err := ms.UpdateNode(f.ctx, &types.MsgUpdateNode{Operator: op, Id: id, Endpoint: "no-port"})
		require.ErrorIs(t, err, types.ErrInvalidEndpoint)
	})

	t.Run("rejects an empty update", func(t *testing.T) {
		_, err := ms.UpdateNode(f.ctx, &types.MsgUpdateNode{Operator: op, Id: id})
		require.ErrorIs(t, err, types.ErrNothingToUpdate)
		_, err = ms.UpdateNode(f.ctx, &types.MsgUpdateNode{Operator: op, Id: id, Endpoint: "203.0.113.5:443", Pubkey: pubkey(3)})
		require.ErrorIs(t, err, types.ErrNothingToUpdate)
	})

	t.Run("rejects an unknown node", func(t *testing.T) {
		_, err := ms.UpdateNode(f.ctx, &types.MsgUpdateNode{Operator: op, Id: 999, Endpoint: "203.0.113.5:443"})
		require.ErrorIs(t, err, types.ErrNodeNotFound)
	})

	t.Run("rejects an unbonding node", func(t *testing.T) {
		_, err := ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: op, Id: other})
		require.NoError(t, err)
		_, err = ms.UpdateNode(f.ctx, &types.MsgUpdateNode{Operator: op, Id: other, Endpoint: "203.0.113.5:443"})
		require.ErrorIs(t, err, types.ErrNodeNotActive)
	})

	f.requireInvariant(t)
}

func TestAddBond(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, 20_000_000_000)
	_, stranger := f.account(t, 2)

	id := register(t, f, ms, op, 1, types.TierEntry, entryBond)

	_, err := ms.AddBond(f.ctx, &types.MsgAddBond{Operator: op, Id: id, Amount: bond(250)})
	require.NoError(t, err)
	node, err := f.keeper.GetNode(f.ctx, id)
	require.NoError(t, err)
	requireCoin(t, bond(entryBond+250), node.Bond)
	f.requireInvariant(t)

	_, err = ms.AddBond(f.ctx, &types.MsgAddBond{Operator: stranger, Id: id, Amount: bond(250)})
	require.ErrorIs(t, err, types.ErrNotOperator)

	_, err = ms.AddBond(f.ctx, &types.MsgAddBond{Operator: op, Id: id, Amount: sdk.NewInt64Coin("token", 250)})
	require.ErrorIs(t, err, types.ErrInvalidBond)

	_, err = ms.AddBond(f.ctx, &types.MsgAddBond{Operator: op, Id: id, Amount: bond(0)})
	require.ErrorIs(t, err, types.ErrInvalidBond)

	_, err = ms.AddBond(f.ctx, &types.MsgAddBond{Operator: op, Id: id, Amount: bond(999_000_000_000)})
	require.ErrorIs(t, err, sdkerrors.ErrInsufficientFunds)

	_, err = ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: op, Id: id})
	require.NoError(t, err)
	_, err = ms.AddBond(f.ctx, &types.MsgAddBond{Operator: op, Id: id, Amount: bond(250)})
	require.ErrorIs(t, err, types.ErrNodeNotActive)

	f.requireInvariant(t)
}

func TestDeregisterAndWithdraw(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, entryBond)
	_, stranger := f.account(t, 2)

	id := register(t, f, ms, op, 1, types.TierEntry, entryBond)
	require.True(t, f.balance(addr).IsZero())

	// an active node cannot withdraw
	_, err := ms.WithdrawBond(f.ctx, &types.MsgWithdrawBond{Operator: op, Id: id})
	require.ErrorIs(t, err, types.ErrNodeNotUnbonding)

	// only the operator can deregister
	_, err = ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: stranger, Id: id})
	require.ErrorIs(t, err, types.ErrNotOperator)

	start := f.sdkCtx.BlockTime().Unix()
	_, err = ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: op, Id: id})
	require.NoError(t, err)

	node, err := f.keeper.GetNode(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, types.StatusUnbonding, node.Status)
	require.Equal(t, start+int64(types.DefaultUnbondingSeconds), node.UnbondingEnd)
	requireCoin(t, bond(entryBond), node.Bond) // still locked

	// deregistering twice is refused
	_, err = ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: op, Id: id})
	require.ErrorIs(t, err, types.ErrNodeNotActive)

	// one second before the end: still locked
	f.advance(time.Duration(types.DefaultUnbondingSeconds-1) * time.Second)
	_, err = ms.WithdrawBond(f.ctx, &types.MsgWithdrawBond{Operator: op, Id: id})
	require.ErrorIs(t, err, types.ErrUnbondingNotOver)
	require.True(t, f.balance(addr).IsZero())

	// exactly at the end: a stranger still cannot take it
	f.advance(time.Second)
	_, err = ms.WithdrawBond(f.ctx, &types.MsgWithdrawBond{Operator: stranger, Id: id})
	require.ErrorIs(t, err, types.ErrNotOperator)

	// the operator can
	_, err = ms.WithdrawBond(f.ctx, &types.MsgWithdrawBond{Operator: op, Id: id})
	require.NoError(t, err)
	requireInt(t, math.NewIntFromUint64(entryBond), f.balance(addr))
	require.True(t, f.moduleBalance().IsZero())

	// the node is gone and cannot be withdrawn twice
	_, err = f.keeper.GetNode(f.ctx, id)
	require.ErrorIs(t, err, types.ErrNodeNotFound)
	_, err = ms.WithdrawBond(f.ctx, &types.MsgWithdrawBond{Operator: op, Id: id})
	require.ErrorIs(t, err, types.ErrNodeNotFound)

	// the public key is free again, and ids are never reused
	newID := register(t, f, ms, op, 1, types.TierEntry, entryBond)
	require.Equal(t, uint64(1), newID)
	f.requireInvariant(t)
}

func TestUnbondingEndIsFixedAtDeregister(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, entryBond)

	id := register(t, f, ms, op, 1, types.TierEntry, entryBond)
	_, err := ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: op, Id: id})
	require.NoError(t, err)
	before, err := f.keeper.GetNode(f.ctx, id)
	require.NoError(t, err)

	// governance later lengthens the period; this node's end must not move
	params := types.DefaultParams()
	params.UnbondingSeconds = types.MaxUnbondingSeconds
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))

	after, err := f.keeper.GetNode(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, before.UnbondingEnd, after.UnbondingEnd)

	f.advance(time.Duration(types.DefaultUnbondingSeconds) * time.Second)
	_, err = ms.WithdrawBond(f.ctx, &types.MsgWithdrawBond{Operator: op, Id: id})
	require.NoError(t, err)
}

func TestSlashNode(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, op := f.account(t, 1)
	f.fund(addr, 20_000_000_000)

	id := register(t, f, ms, op, 1, types.TierEntry, entryBond)
	full := register(t, f, ms, op, 2, types.TierEntry, entryBond)
	spent := f.balance(addr)

	// partial slash of an active node
	burned, err := f.keeper.SlashNode(f.ctx, id, math.NewInt(400))
	require.NoError(t, err)
	requireInt(t, math.NewInt(400), burned)
	node, err := f.keeper.GetNode(f.ctx, id)
	require.NoError(t, err)
	requireCoin(t, bond(entryBond-400), node.Bond)
	requireInt(t, math.NewInt(400), f.bank.burned.AmountOf(bondDenom))
	f.requireInvariant(t)

	// invalid amounts
	_, err = f.keeper.SlashNode(f.ctx, id, math.ZeroInt())
	require.ErrorIs(t, err, types.ErrInvalidSlashAmount)
	_, err = f.keeper.SlashNode(f.ctx, id, math.NewInt(-1))
	require.ErrorIs(t, err, types.ErrInvalidSlashAmount)
	_, err = f.keeper.SlashNode(f.ctx, 999, math.NewInt(1))
	require.ErrorIs(t, err, types.ErrNodeNotFound)

	// an unbonding node is still slashable, and withdraws only the remainder
	_, err = ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: op, Id: id})
	require.NoError(t, err)
	burned, err = f.keeper.SlashNode(f.ctx, id, math.NewInt(600))
	require.NoError(t, err)
	requireInt(t, math.NewInt(600), burned)
	f.requireInvariant(t)

	// a slash larger than the bond burns only what is there
	_, err = ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: op, Id: full})
	require.NoError(t, err)
	burned, err = f.keeper.SlashNode(f.ctx, full, math.NewIntFromUint64(entryBond*10))
	require.NoError(t, err)
	requireInt(t, math.NewIntFromUint64(entryBond), burned)
	burned, err = f.keeper.SlashNode(f.ctx, full, math.NewInt(1))
	require.NoError(t, err)
	require.True(t, burned.IsZero())
	f.requireInvariant(t)

	f.advance(time.Duration(types.DefaultUnbondingSeconds) * time.Second)

	_, err = ms.WithdrawBond(f.ctx, &types.MsgWithdrawBond{Operator: op, Id: id})
	require.NoError(t, err)
	requireInt(t, spent.Add(math.NewIntFromUint64(entryBond-1000)), f.balance(addr))

	// the fully slashed node withdraws nothing, and is still removed
	_, err = ms.WithdrawBond(f.ctx, &types.MsgWithdrawBond{Operator: op, Id: full})
	require.NoError(t, err)
	requireInt(t, spent.Add(math.NewIntFromUint64(entryBond-1000)), f.balance(addr))
	_, err = f.keeper.GetNode(f.ctx, full)
	require.ErrorIs(t, err, types.ErrNodeNotFound)

	require.True(t, f.moduleBalance().IsZero())
	requireInt(t, math.NewIntFromUint64(entryBond+1000), f.bank.burned.AmountOf(bondDenom))
	f.requireInvariant(t)
}

// TestBondInvariantAcrossOperators runs a mixed sequence across several
// operators and checks after every step that no coin is created or lost.
func TestBondInvariantAcrossOperators(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)

	const perOperator = uint64(30_000_000_000)
	const operators = 4
	type op struct {
		addr sdk.AccAddress
		str  string
	}
	ops := make([]op, operators)
	for i := range ops {
		ops[i].addr, ops[i].str = f.account(t, byte(10+i))
		f.fund(ops[i].addr, perOperator)
	}

	conserved := func() {
		t.Helper()
		f.requireInvariant(t)
		sum := f.moduleBalance().Add(f.bank.burned.AmountOf(bondDenom))
		for _, o := range ops {
			sum = sum.Add(f.balance(o.addr))
		}
		requireInt(t, math.NewIntFromUint64(perOperator*operators), sum)
	}

	var ids []uint64
	for i, o := range ops {
		ids = append(ids, register(t, f, ms, o.str, byte(100+i), types.TierEntry, entryBond+uint64(i)))
		conserved()
		ids = append(ids, register(t, f, ms, o.str, byte(200+i), types.TierExit, exitBond+uint64(i)))
		conserved()
	}

	_, err := ms.AddBond(f.ctx, &types.MsgAddBond{Operator: ops[0].str, Id: ids[0], Amount: bond(12345)})
	require.NoError(t, err)
	conserved()

	_, err = f.keeper.SlashNode(f.ctx, ids[3], math.NewInt(777))
	require.NoError(t, err)
	conserved()

	for i := 0; i < len(ids); i += 2 {
		_, err = ms.DeregisterNode(f.ctx, &types.MsgDeregisterNode{Operator: ops[i/2].str, Id: ids[i]})
		require.NoError(t, err)
		conserved()
	}

	_, err = f.keeper.SlashNode(f.ctx, ids[2], math.NewInt(5))
	require.NoError(t, err)
	conserved()

	f.advance(time.Duration(types.DefaultUnbondingSeconds) * time.Second)
	for i := 0; i < len(ids); i += 2 {
		_, err = ms.WithdrawBond(f.ctx, &types.MsgWithdrawBond{Operator: ops[i/2].str, Id: ids[i]})
		require.NoError(t, err)
		conserved()
	}

	// the exit nodes are still active and bonded
	for i := 1; i < len(ids); i += 2 {
		node, err := f.keeper.GetNode(f.ctx, ids[i])
		require.NoError(t, err)
		require.Equal(t, types.StatusActive, node.Status)
	}
}
