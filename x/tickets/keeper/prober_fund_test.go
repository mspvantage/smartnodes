package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// The prober fund pays what it holds and no more, and the escrow account
// always matches the module's records afterwards.
func TestPayFromProberFund(t *testing.T) {
	f := initFixture(t)
	addr, _ := f.account(t, 1)

	// A fund of 100 with the coins to back it.
	require.NoError(t, f.keeper.ProberFund.Set(f.ctx, payDenom, math.NewInt(100)))
	f.bank.modules[types.ModuleName] = sdk.NewCoins(pay(100))
	f.requireEscrowExact(t)

	paid, err := f.keeper.PayFromProberFund(f.ctx, addr, pay(30))
	require.NoError(t, err)
	require.True(t, paid.Equal(pay(30)), "paid %s", paid)
	requireInt(t, 30, f.balance(addr))
	requireInt(t, 70, f.proberFund(t))
	f.requireEscrowExact(t)

	// Asking for more than is left pays what is left.
	paid, err = f.keeper.PayFromProberFund(f.ctx, addr, pay(100))
	require.NoError(t, err)
	require.True(t, paid.Equal(pay(70)), "paid %s", paid)
	requireInt(t, 100, f.balance(addr))
	requireInt(t, 0, f.proberFund(t))
	f.requireEscrowExact(t)

	// An empty fund pays nothing and is not an error.
	paid, err = f.keeper.PayFromProberFund(f.ctx, addr, pay(10))
	require.NoError(t, err)
	require.True(t, paid.IsZero())
	requireInt(t, 100, f.balance(addr))

	// A coin the fund does not hold pays nothing.
	paid, err = f.keeper.PayFromProberFund(f.ctx, addr, sdk.NewCoin("other", math.NewInt(5)))
	require.NoError(t, err)
	require.True(t, paid.IsZero())

	// Asking for zero pays nothing.
	paid, err = f.keeper.PayFromProberFund(f.ctx, addr, pay(0))
	require.NoError(t, err)
	require.True(t, paid.IsZero())
	f.requireEscrowExact(t)
}
