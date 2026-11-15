package keeper_test

import (
	"context"
	"errors"
	"testing"

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
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/mspvantage/smartnodes/x/rewards/keeper"
	module "github.com/mspvantage/smartnodes/x/rewards/module"
	"github.com/mspvantage/smartnodes/x/rewards/types"
)

// mockBank is an in-memory bank. Module accounts are ordinary addresses, as
// in the real one, so a balance read by address and a transfer by module name
// see the same coins.
type mockBank struct {
	balances map[string]sdk.Coins // keyed by raw address bytes
}

func newMockBank() *mockBank { return &mockBank{balances: map[string]sdk.Coins{}} }

func moduleAddr(name string) sdk.AccAddress { return authtypes.NewModuleAddress(name) }

func (m *mockBank) SpendableCoins(_ context.Context, addr sdk.AccAddress) sdk.Coins {
	return m.balances[string(addr)]
}

func (m *mockBank) GetAllBalances(_ context.Context, addr sdk.AccAddress) sdk.Coins {
	return m.balances[string(addr)]
}

func (m *mockBank) move(from, to sdk.AccAddress, amt sdk.Coins) error {
	bal := m.balances[string(from)]
	if !bal.IsAllGTE(amt) {
		return sdkerrors.ErrInsufficientFunds
	}
	m.balances[string(from)] = bal.Sub(amt...)
	m.balances[string(to)] = m.balances[string(to)].Add(amt...)
	return nil
}

func (m *mockBank) SendCoinsFromModuleToModule(_ context.Context, senderModule, recipientModule string, amt sdk.Coins) error {
	return m.move(moduleAddr(senderModule), moduleAddr(recipientModule), amt)
}

// mockStaking knows validators by the address they sign blocks with.
type mockStaking struct {
	byCons map[string]stakingtypes.Validator
}

func (m *mockStaking) ConsensusAddressCodec() address.Codec { return nil }

func (m *mockStaking) ValidatorByConsAddr(_ context.Context, cons sdk.ConsAddress) (stakingtypes.ValidatorI, error) {
	v, ok := m.byCons[string(cons)]
	if !ok {
		return nil, stakingtypes.ErrNoValidatorFound
	}
	return v, nil
}

// mockDistr records what each validator was credited and holds the community
// pool. Like the real module, crediting a validator is bookkeeping only; the
// coins must already be in the distribution module account.
type mockDistr struct {
	bank          *mockBank
	communityTax  math.LegacyDec
	credited      map[string]sdk.DecCoins // keyed by validator operator address
	communityPool sdk.Coins
	failAllocate  bool
}

func (m *mockDistr) GetCommunityTax(context.Context) (math.LegacyDec, error) {
	return m.communityTax, nil
}

func (m *mockDistr) AllocateTokensToValidator(_ context.Context, val stakingtypes.ValidatorI, tokens sdk.DecCoins) error {
	if m.failAllocate {
		return errors.New("allocation refused")
	}
	m.credited[val.GetOperator()] = m.credited[val.GetOperator()].Add(tokens...)
	return nil
}

func (m *mockDistr) FundCommunityPool(_ context.Context, amount sdk.Coins, sender sdk.AccAddress) error {
	if err := m.bank.move(sender, moduleAddr(distrtypes.ModuleName), amount); err != nil {
		return err
	}
	m.communityPool = m.communityPool.Add(amount...)
	return nil
}

type fixture struct {
	ctx          context.Context
	sdkCtx       sdk.Context
	keeper       keeper.Keeper
	addressCodec address.Codec
	bank         *mockBank
	staking      *mockStaking
	distr        *mockDistr
}

func initFixture(t *testing.T) *fixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)

	storeService := runtime.NewKVStoreService(storeKey)
	sdkCtx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_test")).Ctx
	sdkCtx = sdkCtx.WithBlockHeight(10)

	authority := authtypes.NewModuleAddress(types.GovModuleName)

	bank := newMockBank()
	staking := &mockStaking{byCons: map[string]stakingtypes.Validator{}}
	distr := &mockDistr{
		bank:         bank,
		communityTax: math.LegacyNewDecWithPrec(2, 2), // 2%
		credited:     map[string]sdk.DecCoins{},
	}

	k := keeper.NewKeeper(
		storeService,
		encCfg.Codec,
		addressCodec,
		authority,
		bank,
		staking,
		distr,
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
		staking:      staking,
		distr:        distr,
	}
}
