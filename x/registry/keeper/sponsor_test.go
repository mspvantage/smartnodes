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

// sponsorWorld is a fixture with sponsorship switched on, a funded sponsor
// pool, a sponsor account and an operator with no tokens of their own.
type sponsorWorld struct {
	*fixture
	ms          types.MsgServer
	sponsorAddr sdk.AccAddress
	sponsor     string
	opAddr      sdk.AccAddress
	op          string
}

const poolStart = uint64(10_000_000_000) // 10,000 SNODE

func newSponsorWorld(t *testing.T) *sponsorWorld {
	t.Helper()
	f := initFixture(t)
	w := &sponsorWorld{fixture: f, ms: keeper.NewMsgServerImpl(f.keeper)}
	w.sponsorAddr, w.sponsor = f.account(t, 0x51)
	w.opAddr, w.op = f.account(t, 0x52)

	params := types.DefaultParams()
	params.Sponsor = w.sponsor
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	f.bank.modules[types.SponsorPoolName] = sdk.NewCoins(bond(poolStart))
	return w
}

func (w *sponsorWorld) pool() math.Int {
	return w.bank.modules[types.SponsorPoolName].AmountOf(bondDenom)
}

func (w *sponsorWorld) count(t *testing.T) uint64 {
	t.Helper()
	n, err := w.keeper.SponsoredCount.Get(w.ctx)
	if err != nil {
		return 0
	}
	return n
}

func (w *sponsorWorld) sponsorMsg(key byte, tier uint64) *types.MsgSponsorNode {
	return &types.MsgSponsorNode{
		Sponsor:  w.sponsor,
		Operator: w.op,
		Endpoint: "node.example.com:51820",
		Pubkey:   pubkey(key),
		Tier:     tier,
	}
}

// sponsorNode registers a sponsored entry node and requires it to succeed.
func (w *sponsorWorld) sponsorNode(t *testing.T, key byte) uint64 {
	t.Helper()
	res, err := w.ms.SponsorNode(w.ctx, w.sponsorMsg(key, types.TierEntry))
	require.NoError(t, err)
	return res.Id
}

func (w *sponsorWorld) node(t *testing.T, id uint64) types.Node {
	t.Helper()
	n, err := w.keeper.GetNode(w.ctx, id)
	require.NoError(t, err)
	return n
}

// leave takes a node through deregistration, the whole unbonding period and
// withdrawal, signed by the given account.
func (w *sponsorWorld) leave(t *testing.T, signer string, id uint64) {
	t.Helper()
	_, err := w.ms.DeregisterNode(w.ctx, &types.MsgDeregisterNode{Operator: signer, Id: id})
	require.NoError(t, err)
	w.advance(time.Duration(types.DefaultUnbondingSeconds) * time.Second)
	_, err = w.ms.WithdrawBond(w.ctx, &types.MsgWithdrawBond{Operator: signer, Id: id})
	require.NoError(t, err)
}

func u(v uint64) math.Int { return math.NewIntFromUint64(v) }

func TestSponsorshipIsOffByDefault(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	_, sponsor := f.account(t, 1)
	_, op := f.account(t, 2)
	f.bank.modules[types.SponsorPoolName] = sdk.NewCoins(bond(poolStart))

	_, err := ms.SponsorNode(f.ctx, &types.MsgSponsorNode{
		Sponsor: sponsor, Operator: op, Endpoint: "node.example.com:51820", Pubkey: pubkey(1), Tier: types.TierEntry,
	})
	require.ErrorIs(t, err, types.ErrSponsorshipOff)
	require.Empty(t, types.DefaultParams().Sponsor)
}

func TestSponsorNode(t *testing.T) {
	w := newSponsorWorld(t)
	id := w.sponsorNode(t, 1)

	n := w.node(t, id)
	require.Equal(t, w.op, n.Operator)
	require.Equal(t, types.StatusActive, n.Status)
	requireCoin(t, bond(entryBond), n.Bond)
	require.Equal(t, entryBond, n.Sponsored)

	// The bond came from the pool. The operator and the sponsor paid nothing.
	requireInt(t, u(poolStart-entryBond), w.pool())
	requireInt(t, u(entryBond), w.moduleBalance())
	require.True(t, w.balance(w.opAddr).IsZero())
	require.True(t, w.balance(w.sponsorAddr).IsZero())
	require.Equal(t, uint64(1), w.count(t))
	w.requireInvariant(t)

	// An exit node is lent the exit minimum.
	res, err := w.ms.SponsorNode(w.ctx, w.sponsorMsg(2, types.TierExit))
	require.NoError(t, err)
	require.Equal(t, exitBond, w.node(t, res.Id).Sponsored)
	requireInt(t, u(poolStart-entryBond-exitBond), w.pool())
	require.Equal(t, uint64(2), w.count(t))
	w.requireInvariant(t)
}

func TestSponsorNodeRejected(t *testing.T) {
	w := newSponsorWorld(t)
	w.sponsorNode(t, 1)

	cases := []struct {
		name   string
		change func(*types.MsgSponsorNode)
		want   error
	}{
		{"sent by the operator, not the sponsor", func(m *types.MsgSponsorNode) { m.Sponsor = w.op }, types.ErrNotSponsor},
		{"unknown tier", func(m *types.MsgSponsorNode) { m.Tier = 9 }, types.ErrInvalidTier},
		{"bad endpoint", func(m *types.MsgSponsorNode) { m.Endpoint = "nope" }, types.ErrInvalidEndpoint},
		{"bad public key", func(m *types.MsgSponsorNode) { m.Pubkey = "AAAA" }, types.ErrInvalidPubkey},
		{"public key already registered", func(m *types.MsgSponsorNode) { m.Pubkey = pubkey(1) }, types.ErrPubkeyTaken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := w.sponsorMsg(7, types.TierEntry)
			tc.change(m)
			_, err := w.ms.SponsorNode(w.ctx, m)
			require.ErrorIs(t, err, tc.want)
		})
	}
	t.Run("unreadable addresses", func(t *testing.T) {
		m := w.sponsorMsg(7, types.TierEntry)
		m.Operator = "nope"
		_, err := w.ms.SponsorNode(w.ctx, m)
		require.Error(t, err)
		m = w.sponsorMsg(7, types.TierEntry)
		m.Sponsor = "nope"
		_, err = w.ms.SponsorNode(w.ctx, m)
		require.Error(t, err)
	})

	// None of the refused attempts took anything from the pool.
	requireInt(t, u(poolStart-entryBond), w.pool())
	require.Equal(t, uint64(1), w.count(t))
	w.requireInvariant(t)
}

func TestSponsorPoolCannotCoverTheBond(t *testing.T) {
	w := newSponsorWorld(t)
	w.bank.modules[types.SponsorPoolName] = sdk.NewCoins(bond(entryBond - 1))

	_, err := w.ms.SponsorNode(w.ctx, w.sponsorMsg(1, types.TierEntry))
	require.ErrorIs(t, err, sdkerrors.ErrInsufficientFunds)

	// Nothing was stored for the failed attempt.
	taken, err := w.keeper.NodeByPubkey.Has(w.ctx, pubkey(1))
	require.NoError(t, err)
	require.False(t, taken)
	require.Zero(t, w.count(t))
	requireInt(t, u(entryBond-1), w.pool())
}

func TestSponsoredNodeLimit(t *testing.T) {
	w := newSponsorWorld(t)
	params, err := w.keeper.Params.Get(w.ctx)
	require.NoError(t, err)
	params.MaxSponsoredNodes = 2
	require.NoError(t, w.keeper.Params.Set(w.ctx, params))

	a := w.sponsorNode(t, 1)
	w.sponsorNode(t, 2)
	_, err = w.ms.SponsorNode(w.ctx, w.sponsorMsg(3, types.TierEntry))
	require.ErrorIs(t, err, types.ErrSponsorLimit)

	// When a sponsored node leaves, its place is free again.
	w.leave(t, w.op, a)
	require.Equal(t, uint64(1), w.count(t))
	w.sponsorNode(t, 3)
	require.Equal(t, uint64(2), w.count(t))
}

// The whole bond of a sponsored node returns to the pool when it leaves.
func TestSponsoredBondReturnsToThePool(t *testing.T) {
	w := newSponsorWorld(t)
	id := w.sponsorNode(t, 1)
	w.leave(t, w.op, id)

	requireInt(t, u(poolStart), w.pool())
	require.True(t, w.balance(w.opAddr).IsZero(), "the operator must not receive the sponsored bond")
	require.True(t, w.moduleBalance().IsZero())
	require.Zero(t, w.count(t))
	_, err := w.keeper.GetNode(w.ctx, id)
	require.ErrorIs(t, err, types.ErrNodeNotFound)
	w.requireInvariant(t)
}

// Bond the operator adds is their own: it comes back to them, and the
// sponsored part goes back to the pool.
func TestOperatorsOwnBondComesBackToThem(t *testing.T) {
	w := newSponsorWorld(t)
	id := w.sponsorNode(t, 1)
	w.fund(w.opAddr, 500)
	_, err := w.ms.AddBond(w.ctx, &types.MsgAddBond{Operator: w.op, Id: id, Amount: bond(500)})
	require.NoError(t, err)

	n := w.node(t, id)
	requireCoin(t, bond(entryBond+500), n.Bond)
	require.Equal(t, entryBond, n.Sponsored)
	w.requireInvariant(t)

	w.leave(t, w.op, id)
	requireInt(t, u(poolStart), w.pool())
	requireInt(t, u(500), w.balance(w.opAddr))
	w.requireInvariant(t)
}

// A slash burns the operator's own part of the bond first, and only then
// the sponsored part.
func TestSlashBurnsTheOperatorsPartFirst(t *testing.T) {
	t.Run("smaller than the operator's part", func(t *testing.T) {
		w := newSponsorWorld(t)
		id := w.sponsorNode(t, 1)
		w.fund(w.opAddr, 500)
		_, err := w.ms.AddBond(w.ctx, &types.MsgAddBond{Operator: w.op, Id: id, Amount: bond(500)})
		require.NoError(t, err)

		burned, err := w.keeper.SlashNode(w.ctx, id, math.NewInt(300))
		require.NoError(t, err)
		requireInt(t, u(300), burned)
		require.Equal(t, entryBond, w.node(t, id).Sponsored, "the sponsored part is untouched")

		w.leave(t, w.op, id)
		requireInt(t, u(poolStart), w.pool())
		requireInt(t, u(200), w.balance(w.opAddr))
		w.requireInvariant(t)
	})
	t.Run("larger than the operator's part", func(t *testing.T) {
		w := newSponsorWorld(t)
		id := w.sponsorNode(t, 1)
		w.fund(w.opAddr, 500)
		_, err := w.ms.AddBond(w.ctx, &types.MsgAddBond{Operator: w.op, Id: id, Amount: bond(500)})
		require.NoError(t, err)

		_, err = w.keeper.SlashNode(w.ctx, id, math.NewInt(700))
		require.NoError(t, err)
		require.Equal(t, entryBond-200, w.node(t, id).Sponsored)
		require.Equal(t, uint64(1), w.count(t))

		w.leave(t, w.op, id)
		requireInt(t, u(poolStart-200), w.pool())
		require.True(t, w.balance(w.opAddr).IsZero())
		w.requireInvariant(t)
	})
	t.Run("with no bond of the operator's own", func(t *testing.T) {
		w := newSponsorWorld(t)
		id := w.sponsorNode(t, 1)
		_, err := w.keeper.SlashNode(w.ctx, id, math.NewInt(400))
		require.NoError(t, err)
		require.Equal(t, entryBond-400, w.node(t, id).Sponsored)
		w.leave(t, w.op, id)
		requireInt(t, u(poolStart-400), w.pool())
	})
	t.Run("the whole bond", func(t *testing.T) {
		w := newSponsorWorld(t)
		id := w.sponsorNode(t, 1)
		_, err := w.keeper.SlashNode(w.ctx, id, u(entryBond*2))
		require.NoError(t, err)
		n := w.node(t, id)
		require.True(t, n.Bond.IsZero())
		require.Zero(t, n.Sponsored)
		// Nothing is left to return, so the node no longer counts as sponsored.
		require.Zero(t, w.count(t))

		w.leave(t, w.op, id)
		requireInt(t, u(poolStart-entryBond), w.pool())
		require.True(t, w.balance(w.opAddr).IsZero())
		require.Zero(t, w.count(t))
		w.requireInvariant(t)
	})
}

// If the operator walks away, the sponsor can take the node out of service
// and recover the bond. The operator's own part still goes to the operator.
func TestSponsorCanRecoverAnAbandonedNode(t *testing.T) {
	w := newSponsorWorld(t)
	id := w.sponsorNode(t, 1)
	w.fund(w.opAddr, 500)
	_, err := w.ms.AddBond(w.ctx, &types.MsgAddBond{Operator: w.op, Id: id, Amount: bond(500)})
	require.NoError(t, err)

	w.leave(t, w.sponsor, id)

	requireInt(t, u(poolStart), w.pool())
	requireInt(t, u(500), w.balance(w.opAddr))
	require.True(t, w.balance(w.sponsorAddr).IsZero(), "the sponsor account itself receives nothing")
	w.requireInvariant(t)
}

func TestSponsorPowersAreLimited(t *testing.T) {
	w := newSponsorWorld(t)
	sponsored := w.sponsorNode(t, 1)

	// A node the operator bonded themselves is out of the sponsor's reach.
	otherAddr, other := w.account(t, 0x60)
	w.fund(otherAddr, entryBond)
	own := register(t, w.fixture, w.ms, other, 2, types.TierEntry, entryBond)
	_, err := w.ms.DeregisterNode(w.ctx, &types.MsgDeregisterNode{Operator: w.sponsor, Id: own})
	require.ErrorIs(t, err, types.ErrNotOperator)

	// A stranger cannot touch a sponsored node.
	_, err = w.ms.DeregisterNode(w.ctx, &types.MsgDeregisterNode{Operator: other, Id: sponsored})
	require.ErrorIs(t, err, types.ErrNotOperator)

	// The sponsor cannot change a sponsored node's details or add bond in
	// the operator's name.
	_, err = w.ms.UpdateNode(w.ctx, &types.MsgUpdateNode{Operator: w.sponsor, Id: sponsored, Endpoint: "evil.example.com:1"})
	require.ErrorIs(t, err, types.ErrNotOperator)

	// Once the sponsored part has been slashed away, the sponsor has nothing
	// left to recover and loses control of the node.
	_, err = w.keeper.SlashNode(w.ctx, sponsored, u(entryBond))
	require.NoError(t, err)
	_, err = w.ms.DeregisterNode(w.ctx, &types.MsgDeregisterNode{Operator: w.sponsor, Id: sponsored})
	require.ErrorIs(t, err, types.ErrNotOperator)

	// If sponsorship is switched off, the old sponsor loses control too.
	another := w.sponsorNode(t, 3)
	params, err := w.keeper.Params.Get(w.ctx)
	require.NoError(t, err)
	params.Sponsor = ""
	require.NoError(t, w.keeper.Params.Set(w.ctx, params))
	_, err = w.ms.DeregisterNode(w.ctx, &types.MsgDeregisterNode{Operator: w.sponsor, Id: another})
	require.ErrorIs(t, err, types.ErrNotOperator)
	// The operator can still leave, and the bond still returns to the pool.
	before := w.pool()
	w.leave(t, w.op, another)
	requireInt(t, before.Add(u(entryBond)), w.pool())
}

func TestFundSponsorPool(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	addr, str := f.account(t, 1)
	f.fund(addr, 5_000)

	_, err := ms.FundSponsorPool(f.ctx, &types.MsgFundSponsorPool{Sender: str, Amount: 3_000})
	require.NoError(t, err)
	requireInt(t, u(3_000), f.bank.modules[types.SponsorPoolName].AmountOf(bondDenom))
	requireInt(t, u(2_000), f.balance(addr))
	// The pool is separate from the account that holds bonds.
	require.True(t, f.moduleBalance().IsZero())

	_, err = ms.FundSponsorPool(f.ctx, &types.MsgFundSponsorPool{Sender: str, Amount: 0})
	require.ErrorIs(t, err, types.ErrInvalidAmount)
	_, err = ms.FundSponsorPool(f.ctx, &types.MsgFundSponsorPool{Sender: str, Amount: 2_001})
	require.ErrorIs(t, err, sdkerrors.ErrInsufficientFunds)
	_, err = ms.FundSponsorPool(f.ctx, &types.MsgFundSponsorPool{Sender: "nope", Amount: 1})
	require.Error(t, err)
}

func TestSponsorSettings(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	authority, err := f.addressCodec.BytesToString(f.keeper.GetAuthority())
	require.NoError(t, err)
	_, sponsor := f.account(t, 1)

	params := types.DefaultParams()
	params.Sponsor = "not-an-address"
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: authority, Params: params})
	require.ErrorIs(t, err, types.ErrInvalidParams)

	params.Sponsor = sponsor
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Authority: authority, Params: params})
	require.NoError(t, err)
	got, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, sponsor, got.Sponsor)
	require.Equal(t, types.DefaultMaxSponsoredNodes, got.MaxSponsoredNodes)
}

func TestSponsoredGenesis(t *testing.T) {
	w := newSponsorWorld(t)
	w.sponsorNode(t, 1)
	w.sponsorNode(t, 2)
	otherAddr, other := w.account(t, 0x60)
	w.fund(otherAddr, entryBond)
	register(t, w.fixture, w.ms, other, 3, types.TierEntry, entryBond)

	exported, err := w.keeper.ExportGenesis(w.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())

	// A fresh chain rebuilds the count of sponsored nodes from the nodes.
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *exported))
	n, err := f.keeper.SponsoredCount.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), n)

	// A node whose sponsored part is larger than its bond is refused.
	bad := types.Node{
		Operator: other, Endpoint: "a.example.com:51820", Pubkey: pubkey(9), Tier: types.TierEntry,
		Bond: bond(100), Status: types.StatusActive, Sponsored: 101,
	}
	require.ErrorIs(t, bad.ValidateBasic(), types.ErrInvalidBond)
	bad.Sponsored = 100
	require.NoError(t, bad.ValidateBasic())

	// A genesis file with an unreadable sponsor address must not start.
	exported.Params.Sponsor = "not-an-address"
	g := initFixture(t)
	require.ErrorIs(t, g.keeper.InitGenesis(g.ctx, *exported), types.ErrInvalidParams)
}
