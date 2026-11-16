package keeper_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"
	"time"

	"cosmossdk.io/core/address"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/verify/keeper"
	module "github.com/mspvantage/smartnodes/x/verify/module"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

const (
	bondDenom = "usnode"
	testChain = "smartnodes-test"
	// testRoundBlocks is a short round so tests can cross rounds quickly.
	testRoundBlocks = 10
	startHeight     = 10
)

// mockBank is an in-memory stand-in for the bank module. It refuses to move
// coins that are not there, so the tests exercise real balance rules.
type mockBank struct {
	accounts map[string]sdk.Coins // keyed by raw address bytes
	modules  map[string]sdk.Coins // keyed by module name
}

func newMockBank() *mockBank {
	return &mockBank{accounts: map[string]sdk.Coins{}, modules: map[string]sdk.Coins{}}
}

func (m *mockBank) SpendableCoins(_ context.Context, addr sdk.AccAddress) sdk.Coins {
	return m.accounts[string(addr)]
}

// GetBalance returns an account's balance. The node rewards pool is the only
// module account the verify module reads this way.
func (m *mockBank) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	if addr.Equals(keeper.PoolAddress()) {
		return sdk.NewCoin(denom, m.modules[types.RewardsPoolName].AmountOf(denom))
	}
	return sdk.NewCoin(denom, m.accounts[string(addr)].AmountOf(denom))
}

func (m *mockBank) SendCoinsFromAccountToModule(_ context.Context, sender sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	bal := m.accounts[string(sender)]
	if !bal.IsAllGTE(amt) {
		return sdkerrors.ErrInsufficientFunds
	}
	m.accounts[string(sender)] = bal.Sub(amt...)
	m.modules[recipientModule] = m.modules[recipientModule].Add(amt...)
	return nil
}

func (m *mockBank) SendCoinsFromModuleToAccount(_ context.Context, senderModule string, recipient sdk.AccAddress, amt sdk.Coins) error {
	bal := m.modules[senderModule]
	if !bal.IsAllGTE(amt) {
		return sdkerrors.ErrInsufficientFunds
	}
	m.modules[senderModule] = bal.Sub(amt...)
	m.accounts[string(recipient)] = m.accounts[string(recipient)].Add(amt...)
	return nil
}

// mockRegistry is an in-memory node registry.
type mockRegistry struct {
	nodes  map[uint64]registrytypes.Node
	burned math.Int
	// failSlash makes SlashNode fail, to test that one node's failed tally
	// does not stop the others.
	failSlash bool
}

func (m *mockRegistry) GetNode(_ context.Context, id uint64) (registrytypes.Node, error) {
	node, ok := m.nodes[id]
	if !ok {
		return registrytypes.Node{}, errorsmod.Wrapf(registrytypes.ErrNodeNotFound, "id %d", id)
	}
	return node, nil
}

func (m *mockRegistry) BondDenom(context.Context) (string, error) {
	return bondDenom, nil
}

// exitMinBond is the mock registry's minimum bond for exit nodes; entry
// nodes use nodeBond.
const exitMinBond = uint64(5_000_000_000)

func (m *mockRegistry) MinBondFor(_ context.Context, tier uint64) (uint64, error) {
	switch tier {
	case registrytypes.TierEntry:
		return nodeBond, nil
	case registrytypes.TierExit:
		return exitMinBond, nil
	}
	return 0, registrytypes.ValidateTier(tier)
}

// SlashNode burns bond like the real registry: at most what the node has.
func (m *mockRegistry) SlashNode(_ context.Context, id uint64, amount math.Int) (math.Int, error) {
	if m.failSlash {
		return math.ZeroInt(), sdkerrors.ErrInvalidRequest
	}
	node, ok := m.nodes[id]
	if !ok {
		return math.ZeroInt(), errorsmod.Wrapf(registrytypes.ErrNodeNotFound, "id %d", id)
	}
	burn := math.MinInt(amount, node.Bond.Amount)
	node.Bond = node.Bond.SubAmount(burn)
	m.nodes[id] = node
	m.burned = m.burned.Add(burn)
	return burn, nil
}

// mockTickets is an in-memory prober fund. Like the real one, it pays what
// it holds and no more.
type mockTickets struct {
	bank *mockBank
	fund math.Int
	// failPay makes every payment fail, to test that a failed payment
	// cannot undo a tally.
	failPay bool
	// failFund makes funding the prober fund fail, to test that a failed
	// reward budget cannot stop a round from starting.
	failFund bool
}

// FundProberFund moves coins from a module account into the fund.
func (m *mockTickets) FundProberFund(_ context.Context, fromModule string, amount sdk.Coin) error {
	if m.failFund {
		return sdkerrors.ErrInvalidRequest
	}
	bal := m.bank.modules[fromModule]
	if !bal.IsAllGTE(sdk.NewCoins(amount)) {
		return sdkerrors.ErrInsufficientFunds
	}
	m.bank.modules[fromModule] = bal.Sub(amount)
	m.fund = m.fund.Add(amount.Amount)
	return nil
}

func (m *mockTickets) PayFromProberFund(_ context.Context, recipient sdk.AccAddress, want sdk.Coin) (sdk.Coin, error) {
	if m.failPay {
		return sdk.Coin{}, sdkerrors.ErrInvalidRequest
	}
	pay := sdk.NewCoin(want.Denom, math.MinInt(want.Amount, m.fund))
	if pay.IsPositive() {
		m.fund = m.fund.Sub(pay.Amount)
		m.bank.accounts[string(recipient)] = m.bank.accounts[string(recipient)].Add(pay)
	}
	return pay, nil
}

type fixture struct {
	ctx          context.Context
	sdkCtx       sdk.Context
	keeper       keeper.Keeper
	addressCodec address.Codec
	bank         *mockBank
	registry     *mockRegistry
	tickets      *mockTickets
	ms           types.MsgServer
	qs           types.QueryServer
}

func initFixture(t *testing.T) *fixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)

	storeService := runtime.NewKVStoreService(storeKey)
	sdkCtx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_test")).Ctx
	sdkCtx = sdkCtx.
		WithChainID(testChain).
		WithBlockHeight(startHeight).
		WithBlockTime(time.Unix(1_800_000_000, 0).UTC())

	authority := authtypes.NewModuleAddress(types.GovModuleName)

	bank := newMockBank()
	registry := &mockRegistry{nodes: map[uint64]registrytypes.Node{}, burned: math.ZeroInt()}
	tickets := &mockTickets{bank: bank, fund: math.ZeroInt()}

	k := keeper.NewKeeper(
		storeService,
		encCfg.Codec,
		addressCodec,
		authority,
		bank,
		registry,
		tickets,
	)

	// Default parameters, but with a short round.
	params := types.DefaultParams()
	params.RoundBlocks = testRoundBlocks
	if err := k.Params.Set(sdkCtx, params); err != nil {
		t.Fatalf("failed to set params: %v", err)
	}

	return &fixture{
		ctx:          sdkCtx,
		sdkCtx:       sdkCtx,
		keeper:       k,
		addressCodec: addressCodec,
		bank:         bank,
		registry:     registry,
		tickets:      tickets,
		ms:           keeper.NewMsgServerImpl(k),
		qs:           keeper.NewQueryServerImpl(k),
	}
}

// begin runs the start-of-block work for the current block.
func (f *fixture) begin(t *testing.T) {
	t.Helper()
	require.NoError(t, f.keeper.BeginBlocker(f.ctx))
}

// nextBlocks moves the chain forward by n blocks of five seconds, running
// the start-of-block work for each new block, as the real chain would.
func (f *fixture) nextBlocks(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		f.sdkCtx = f.sdkCtx.
			WithBlockHeight(f.sdkCtx.BlockHeight() + 1).
			WithBlockTime(f.sdkCtx.BlockTime().Add(5 * time.Second))
		f.ctx = f.sdkCtx
		f.begin(t)
	}
}

// nextRound moves forward exactly one round.
func (f *fixture) nextRound(t *testing.T) {
	t.Helper()
	f.nextBlocks(t, testRoundBlocks)
}

// advanceTime moves block time forward without producing blocks.
func (f *fixture) advanceTime(d time.Duration) {
	f.sdkCtx = f.sdkCtx.WithBlockTime(f.sdkCtx.BlockTime().Add(d))
	f.ctx = f.sdkCtx
}

// scratch returns a throwaway copy of the state. A real transaction that
// fails is rolled back; tests use this for calls that are expected to fail,
// so a half-finished failure cannot leak into later checks.
func (f *fixture) scratch() context.Context {
	cc, _ := f.sdkCtx.CacheContext()
	return cc
}

func (f *fixture) setParams(t *testing.T, change func(*types.Params)) {
	t.Helper()
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	change(&params)
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
}

func (f *fixture) round(t *testing.T) uint64 {
	t.Helper()
	round, _, err := f.keeper.CurrentRound(f.ctx)
	require.NoError(t, err)
	return round
}

// account returns a deterministic test account: its raw bytes and text form.
func (f *fixture) account(t *testing.T, seed byte) (sdk.AccAddress, string) {
	t.Helper()
	bz := bytes.Repeat([]byte{seed}, 20)
	str, err := f.addressCodec.BytesToString(bz)
	require.NoError(t, err)
	return bz, str
}

func (f *fixture) fund(addr sdk.AccAddress, amount uint64) {
	f.bank.accounts[string(addr)] = f.bank.accounts[string(addr)].Add(coin(amount))
}

func (f *fixture) balance(addr sdk.AccAddress) math.Int {
	return f.bank.accounts[string(addr)].AmountOf(bondDenom)
}

func (f *fixture) moduleBalance() math.Int {
	return f.bank.modules[types.ModuleName].AmountOf(bondDenom)
}

// nodeBond is the bond every test node starts with: 1,000 SNODE.
const nodeBond = uint64(1_000_000_000)

// addNode puts a node in the mock registry, run by a fixed test operator.
func (f *fixture) addNode(id uint64, status uint64) {
	operator, _ := f.addressCodec.BytesToString(bytes.Repeat([]byte{0xA0}, 20))
	f.registry.nodes[id] = registrytypes.Node{
		Id: id, Operator: operator, Tier: registrytypes.TierEntry, Status: status, Bond: coin(nodeBond),
	}
}

// operator returns the address that runs every test node.
func (f *fixture) operator() string {
	operator, _ := f.addressCodec.BytesToString(bytes.Repeat([]byte{0xA0}, 20))
	return operator
}

// fundPool puts coins in the node rewards pool, as genesis would.
func (f *fixture) fundPool(amount uint64) {
	f.bank.modules[types.RewardsPoolName] = f.bank.modules[types.RewardsPoolName].Add(coin(amount))
}

func (f *fixture) poolBalance() math.Int {
	return f.bank.modules[types.RewardsPoolName].AmountOf(bondDenom)
}

// requirePoolSolvent checks the two rules the reward pool rests on: the
// total owed equals the sum of the nodes' unclaimed balances, and the pool
// account holds at least that much.
func (f *fixture) requirePoolSolvent(t *testing.T) {
	t.Helper()
	owed, err := f.keeper.RewardOwed.Get(f.ctx)
	if err != nil {
		owed = 0
	}
	var sum uint64
	require.NoError(t, f.keeper.NodeRecord.Walk(f.ctx, nil, func(_ uint64, rec types.NodeRecord) (bool, error) {
		sum += rec.RewardBalance
		return false, nil
	}))
	require.Equal(t, sum, owed, "owed total does not match the nodes' balances")
	require.True(t, f.poolBalance().GTE(math.NewIntFromUint64(owed)), "pool holds %s but owes %d", f.poolBalance(), owed)
}

// nodeBondLeft returns what is left of a test node's bond.
func (f *fixture) nodeBondLeft(id uint64) math.Int {
	return f.registry.nodes[id].Bond.Amount
}

// hasEvent reports whether an event of the given type has been emitted.
func (f *fixture) hasEvent(eventType string) bool {
	for _, e := range f.sdkCtx.EventManager().Events() {
		if e.Type == eventType {
			return true
		}
	}
	return false
}

// requireBondsExact checks the rule prober bonds rest on: the module account
// holds exactly the sum of the recorded bonds.
func (f *fixture) requireBondsExact(t *testing.T) {
	t.Helper()
	total, err := f.keeper.TotalBonded(f.ctx)
	require.NoError(t, err)
	require.True(t, total.Equal(f.moduleBalance()), "records say bonds total %s, account holds %s", total, f.moduleBalance())
}

// requireActiveConsistent checks that the active list is dense, that list
// and index agree, and that exactly the probers with an active or leaving
// status are in it.
func (f *fixture) requireActiveConsistent(t *testing.T) {
	t.Helper()
	count, err := f.keeper.ActiveCount.Get(f.ctx)
	if err != nil {
		count = 0
	}
	inList := map[uint64]bool{}
	for idx := uint64(0); idx < count; idx++ {
		id, err := f.keeper.ActiveList.Get(f.ctx, idx)
		require.NoError(t, err, "gap in the active list at position %d", idx)
		require.False(t, inList[id], "prober %d is in the active list twice", id)
		inList[id] = true
		back, err := f.keeper.ActiveIndex.Get(f.ctx, id)
		require.NoError(t, err)
		require.Equal(t, idx, back, "index of prober %d", id)
	}
	hasExtra, err := f.keeper.ActiveList.Has(f.ctx, count)
	require.NoError(t, err)
	require.False(t, hasExtra, "active list has an entry past its length")

	listed := 0
	require.NoError(t, f.keeper.Prober.Walk(f.ctx, nil, func(id uint64, p types.Prober) (bool, error) {
		shouldBe := p.Status == types.ProberActive || p.Status == types.ProberLeaving
		require.Equal(t, shouldBe, inList[id], "prober %d with status %d", id, p.Status)
		if shouldBe {
			listed++
		}
		return false, nil
	}))
	require.Equal(t, int(count), listed)
}

func coin(amount uint64) sdk.Coin {
	return sdk.NewCoin(bondDenom, math.NewIntFromUint64(amount))
}

func requireInt(t *testing.T, want uint64, got math.Int) {
	t.Helper()
	require.True(t, math.NewIntFromUint64(want).Equal(got), "want %d, got %s", want, got)
}

// tester is one prober operator in a test.
type tester struct {
	addr sdk.AccAddress
	str  string
	id   uint64
}

// newProber funds an account and registers it as a prober with the minimum
// bond. The prober joins the active set when the next round starts.
func (f *fixture) newProber(t *testing.T, seed byte) *tester {
	t.Helper()
	addr, str := f.account(t, seed)
	f.fund(addr, types.DefaultMinProberBond)
	res, err := f.ms.RegisterProber(f.ctx, &types.MsgRegisterProber{Owner: str, Bond: types.DefaultMinProberBond})
	require.NoError(t, err)
	return &tester{addr: addr, str: str, id: res.Id}
}

func (f *fixture) prober(t *testing.T, id uint64) types.Prober {
	t.Helper()
	p, err := f.keeper.Prober.Get(f.ctx, id)
	require.NoError(t, err)
	return p
}

// salt returns a fixed, valid salt that differs per prober and node.
func salt(proberID, nodeID uint64) []byte {
	return bytes.Repeat([]byte{byte(proberID*16 + nodeID + 1)}, types.MinSaltLength)
}

// seal builds the sealed form of a report as a prober's software would.
func seal(round, nodeID, proberID, result, speed uint64) string {
	return hex.EncodeToString(types.CommitHash(testChain, round, nodeID, proberID, result, speed, salt(proberID, nodeID)))
}

// commit posts one sealed report and requires it to be accepted.
func (f *fixture) commit(t *testing.T, p *tester, round, nodeID, result, speed uint64) {
	t.Helper()
	_, err := f.ms.CommitReports(f.ctx, &types.MsgCommitReports{
		Owner:   p.str,
		Round:   round,
		NodeIds: []uint64{nodeID},
		Hashes:  []string{seal(round, nodeID, p.id, result, speed)},
	})
	require.NoError(t, err)
}

// revealMsg builds the message that opens a report sealed with seal.
func revealMsg(p *tester, round, nodeID, result, speed uint64) *types.MsgRevealReport {
	return &types.MsgRevealReport{
		Owner:     p.str,
		Round:     round,
		NodeId:    nodeID,
		Result:    result,
		SpeedBand: speed,
		Salt:      hex.EncodeToString(salt(p.id, nodeID)),
	}
}

// reveal opens one report and requires it to be accepted.
func (f *fixture) reveal(t *testing.T, p *tester, round, nodeID, result, speed uint64) {
	t.Helper()
	_, err := f.ms.RevealReport(f.ctx, revealMsg(p, round, nodeID, result, speed))
	require.NoError(t, err)
}

// reportCount returns how many reports are stored.
func (f *fixture) reportCount(t *testing.T) int {
	t.Helper()
	n := 0
	iter, err := f.keeper.Reports.Iterate(f.ctx, nil)
	require.NoError(t, err)
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		n++
	}
	return n
}

// world is a fixture in round 1 with three active probers and two active
// nodes. With three probers and three slots per node, every prober is
// assigned to every node.
type world struct {
	*fixture
	p [3]*tester
}

func newWorld(t *testing.T) *world {
	t.Helper()
	f := initFixture(t)
	w := &world{fixture: f}
	for i := range w.p {
		w.p[i] = f.newProber(t, byte(0x10+i))
	}
	f.addNode(1, registrytypes.StatusActive)
	f.addNode(2, registrytypes.StatusActive)
	f.begin(t) // starts round 1; the three probers join
	require.Equal(t, uint64(1), f.round(t))
	f.requireActiveConsistent(t)
	return w
}

// play runs one full cycle for a node: every prober seals the given result
// in the current round, opens it in the next, and the tally runs in the one
// after. A result of 0 means that prober seals but never opens; a result of
// 99 means that prober does not report at all. It returns the round the
// reports were for.
func (w *world) play(t *testing.T, nodeID uint64, results [3]uint64) uint64 {
	t.Helper()
	round := w.round(t)
	for i, r := range results {
		switch r {
		case 99:
		case 0:
			w.commit(t, w.p[i], round, nodeID, types.ResultPass, 0)
		default:
			w.commit(t, w.p[i], round, nodeID, r, 2)
		}
	}
	w.nextRound(t)
	for i, r := range results {
		if r != 0 && r != 99 {
			w.reveal(t, w.p[i], round, nodeID, r, 2)
		}
	}
	w.nextRound(t)
	return round
}

func (w *world) record(t *testing.T, nodeID uint64) types.NodeRecord {
	t.Helper()
	rec, err := w.keeper.NodeRecord.Get(w.ctx, nodeID)
	require.NoError(t, err)
	return rec
}
