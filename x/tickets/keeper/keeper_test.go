package keeper_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
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
	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
	"github.com/mspvantage/smartnodes/x/tickets/keeper"
	module "github.com/mspvantage/smartnodes/x/tickets/module"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

const (
	payDenom  = "usnode"
	testChain = "smartnodes-test"
	// ticketPrice is one SNODE.
	ticketPrice = uint64(1_000_000)
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
	nodes map[uint64]registrytypes.Node
}

func (m *mockRegistry) GetNode(_ context.Context, id uint64) (registrytypes.Node, error) {
	node, ok := m.nodes[id]
	if !ok {
		return registrytypes.Node{}, errorsmod.Wrapf(registrytypes.ErrNodeNotFound, "id %d", id)
	}
	return node, nil
}

type fixture struct {
	ctx          context.Context
	sdkCtx       sdk.Context
	keeper       keeper.Keeper
	addressCodec address.Codec
	bank         *mockBank
	registry     *mockRegistry
	authority    string
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
		WithBlockHeight(10).
		WithBlockTime(time.Unix(1_800_000_000, 0).UTC())

	authority := authtypes.NewModuleAddress(types.GovModuleName)
	authorityStr, err := addressCodec.BytesToString(authority)
	require.NoError(t, err)

	bank := newMockBank()
	registry := &mockRegistry{nodes: map[uint64]registrytypes.Node{}}

	k := keeper.NewKeeper(
		storeService,
		encCfg.Codec,
		addressCodec,
		authority,
		bank,
		registry,
	)

	// Initialize params
	if err := k.Params.Set(sdkCtx, types.DefaultParams()); err != nil {
		t.Fatalf("failed to set params: %v", err)
	}

	return &fixture{
		ctx:          sdkCtx,
		sdkCtx:       sdkCtx,
		keeper:       k,
		addressCodec: addressCodec,
		bank:         bank,
		registry:     registry,
		authority:    authorityStr,
	}
}

// nextBlocks moves the chain forward by n blocks of five seconds and runs the
// end-of-block queue for each, as the real chain would.
func (f *fixture) nextBlocks(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		require.NoError(t, f.keeper.ProcessQueue(f.ctx))
		f.sdkCtx = f.sdkCtx.
			WithBlockHeight(f.sdkCtx.BlockHeight() + 1).
			WithBlockTime(f.sdkCtx.BlockTime().Add(5 * time.Second))
		f.ctx = f.sdkCtx
	}
}

// advanceTime moves block time forward without running the queue.
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

// account returns a deterministic test account: its raw bytes and text form.
func (f *fixture) account(t *testing.T, seed byte) (sdk.AccAddress, string) {
	t.Helper()
	bz := bytes.Repeat([]byte{seed}, 20)
	str, err := f.addressCodec.BytesToString(bz)
	require.NoError(t, err)
	return bz, str
}

func (f *fixture) fund(addr sdk.AccAddress, amount uint64) {
	f.bank.accounts[string(addr)] = f.bank.accounts[string(addr)].Add(pay(amount))
}

func (f *fixture) balance(addr sdk.AccAddress) math.Int {
	return f.bank.accounts[string(addr)].AmountOf(payDenom)
}

func (f *fixture) escrow() math.Int {
	return f.bank.modules[types.ModuleName].AmountOf(payDenom)
}

// addNode puts a node in the mock registry.
func (f *fixture) addNode(id uint64, operator string, status uint64) {
	f.registry.nodes[id] = registrytypes.Node{Id: id, Operator: operator, Tier: registrytypes.TierEntry, Status: status}
}

// requireEscrowExact checks the rule the module rests on: the escrow account
// holds exactly what the module's own records say it should.
func (f *fixture) requireEscrowExact(t *testing.T) {
	t.Helper()
	expected, err := f.keeper.EscrowExpected(f.ctx)
	require.NoError(t, err)
	want := expected.AmountOf(payDenom)
	require.True(t, want.Equal(f.escrow()), "records say escrow should hold %s, account holds %s", want, f.escrow())
}

func pay(amount uint64) sdk.Coin {
	return sdk.NewCoin(payDenom, math.NewIntFromUint64(amount))
}

func requireInt(t *testing.T, want uint64, got math.Int) {
	t.Helper()
	require.True(t, math.NewIntFromUint64(want).Equal(got), "want %d, got %s", want, got)
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// issuer is a ticket issuer with a real key pair.
type issuer struct {
	sk, pk []byte
}

func newIssuer(t *testing.T, seed byte) issuer {
	t.Helper()
	sk, pk, err := ticketcrypto.GenerateIssuerKey(bytes.Repeat([]byte{seed}, 32))
	require.NoError(t, err)
	return issuer{sk: sk, pk: pk}
}

// sign produces the blind signatures an honest issuer would post.
func (i issuer) sign(t *testing.T, blinded []string) []string {
	t.Helper()
	out := make([]string, len(blinded))
	for n, text := range blinded {
		raw, err := base64.StdEncoding.DecodeString(text)
		require.NoError(t, err)
		sig, err := ticketcrypto.BlindSign(i.sk, raw)
		require.NoError(t, err)
		out[n] = b64(sig)
	}
	return out
}

// wallet is a buyer's set of ticket secrets for one issuer key.
type wallet struct {
	keyID   uint64
	secrets []*ticketcrypto.TicketSecret
	sigs    [][]byte // unblinded issuer signatures, filled by unblind
}

func newWallet(t *testing.T, keyID uint64, n int) *wallet {
	t.Helper()
	w := &wallet{keyID: keyID}
	for i := 0; i < n; i++ {
		s, err := ticketcrypto.NewTicket(rand.Reader, testChain, keyID)
		require.NoError(t, err)
		w.secrets = append(w.secrets, s)
	}
	return w
}

func (w *wallet) blinded() []string {
	out := make([]string, len(w.secrets))
	for i, s := range w.secrets {
		out[i] = b64(s.Blinded)
	}
	return out
}

// unblind turns the issuer's blind signatures into spendable ones.
func (w *wallet) unblind(t *testing.T, pk []byte, blindSigs []string) {
	t.Helper()
	w.sigs = make([][]byte, len(w.secrets))
	for i, s := range w.secrets {
		raw, err := base64.StdEncoding.DecodeString(blindSigs[i])
		require.NoError(t, err)
		sig, err := ticketcrypto.Unblind(s, pk, testChain, w.keyID, raw)
		require.NoError(t, err)
		w.sigs[i] = sig
	}
}

// spend builds ticket i payable to nodeID, as the client would hand it over.
func (w *wallet) spend(t *testing.T, i int, nodeID uint64) string {
	t.Helper()
	ticket, err := ticketcrypto.Spend(w.secrets[i], w.sigs[i], testChain, w.keyID, nodeID)
	require.NoError(t, err)
	return b64(ticket)
}
