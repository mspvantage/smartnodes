package keeper_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"cosmossdk.io/core/address"
	"cosmossdk.io/math"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/registry/keeper"
	module "github.com/mspvantage/smartnodes/x/registry/module"
	"github.com/mspvantage/smartnodes/x/registry/types"
)

// bondDenom is the staking token used throughout the tests.
const bondDenom = "usnode"

// mockBank is an in-memory stand-in for the bank module. It refuses to move
// coins an account does not have, so the tests exercise real balance rules.
type mockBank struct {
	accounts map[string]sdk.Coins // keyed by raw address bytes
	modules  map[string]sdk.Coins // keyed by module name
	burned   sdk.Coins
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

func (m *mockBank) BurnCoins(_ context.Context, moduleName string, amt sdk.Coins) error {
	bal := m.modules[moduleName]
	if !bal.IsAllGTE(amt) {
		return sdkerrors.ErrInsufficientFunds
	}
	m.modules[moduleName] = bal.Sub(amt...)
	m.burned = m.burned.Add(amt...)
	return nil
}

// mockStaking supplies the bond denom.
type mockStaking struct{}

func (mockStaking) ConsensusAddressCodec() address.Codec { return nil }

func (mockStaking) ValidatorByConsAddr(context.Context, sdk.ConsAddress) (stakingtypes.ValidatorI, error) {
	return nil, nil
}

func (mockStaking) BondDenom(context.Context) (string, error) { return bondDenom, nil }

type fixture struct {
	ctx          context.Context
	sdkCtx       sdk.Context
	keeper       keeper.Keeper
	addressCodec address.Codec
	bank         *mockBank
}

func initFixture(t *testing.T) *fixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)

	storeService := runtime.NewKVStoreService(storeKey)
	sdkCtx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_test")).Ctx
	sdkCtx = sdkCtx.WithBlockTime(time.Unix(1_800_000_000, 0).UTC())

	authority := authtypes.NewModuleAddress(types.GovModuleName)
	bank := newMockBank()

	k := keeper.NewKeeper(
		storeService,
		encCfg.Codec,
		addressCodec,
		authority,
		bank,
		mockStaking{},
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
	}
}

// advance moves block time forward.
func (f *fixture) advance(d time.Duration) {
	f.sdkCtx = f.sdkCtx.WithBlockTime(f.sdkCtx.BlockTime().Add(d))
	f.ctx = f.sdkCtx
}

// account returns a deterministic test account: its raw bytes and text form.
func (f *fixture) account(t *testing.T, seed byte) (sdk.AccAddress, string) {
	t.Helper()
	bz := make([]byte, 20)
	for i := range bz {
		bz[i] = seed
	}
	str, err := f.addressCodec.BytesToString(bz)
	require.NoError(t, err)
	return bz, str
}

// fund gives an account an amount of the bond token.
func (f *fixture) fund(addr sdk.AccAddress, amount uint64) {
	f.bank.accounts[string(addr)] = f.bank.accounts[string(addr)].Add(bond(amount))
}

// balance returns an account's bond-token balance.
func (f *fixture) balance(addr sdk.AccAddress) math.Int {
	return f.bank.accounts[string(addr)].AmountOf(bondDenom)
}

// moduleBalance returns the registry module account's bond-token balance.
func (f *fixture) moduleBalance() math.Int {
	return f.bank.modules[types.ModuleName].AmountOf(bondDenom)
}

// requireInvariant checks the rule the whole module rests on: the registry
// module account holds exactly the sum of all node bonds.
func (f *fixture) requireInvariant(t *testing.T) {
	t.Helper()
	total, err := f.keeper.TotalBonded(f.ctx)
	require.NoError(t, err)
	require.True(t, total.Equal(f.moduleBalance()),
		"sum of node bonds %s != registry module balance %s", total, f.moduleBalance())
}

// bond builds a coin of the bond token.
func bond(amount uint64) sdk.Coin {
	return sdk.NewCoin(bondDenom, math.NewIntFromUint64(amount))
}

// pubkey returns a valid, deterministic node public key.
func pubkey(seed byte) string {
	bz := make([]byte, types.PubkeyLength)
	for i := range bz {
		bz[i] = seed
	}
	return base64.StdEncoding.EncodeToString(bz)
}

// requireInt compares two big-number amounts by value.
func requireInt(t *testing.T, want, got math.Int) {
	t.Helper()
	require.True(t, want.Equal(got), "want %s, got %s", want, got)
}

// requireCoin compares two coins by denom and amount.
func requireCoin(t *testing.T, want, got sdk.Coin) {
	t.Helper()
	require.True(t, want.Denom == got.Denom && want.Amount.Equal(got.Amount), "want %s, got %s", want, got)
}
