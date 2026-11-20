package types

import (
	"context"

	"cosmossdk.io/core/address"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
)

// RegistryKeeper defines what the verify module needs from the node registry.
type RegistryKeeper interface {
	// GetNode returns a registered node, or an error if it does not exist.
	GetNode(ctx context.Context, id uint64) (registrytypes.Node, error)
	// BondDenom returns the staking token denom. Prober bonds must use it.
	BondDenom(ctx context.Context) (string, error)
	// SlashNode burns up to amount from a node's bond and returns what was
	// burned.
	SlashNode(ctx context.Context, id uint64, amount math.Int) (math.Int, error)
	// MinBondFor returns the minimum bond of a tier. A node's reward cap is
	// sized from it.
	MinBondFor(ctx context.Context, tier uint64) (uint64, error)
	// WalkNodes calls fn for every registered node, in id order, until fn
	// returns true or an error.
	WalkNodes(ctx context.Context, fn func(registrytypes.Node) (stop bool, err error)) error
}

// TicketsKeeper defines what the verify module needs from the tickets module.
//
// This interface must never be left empty: the app wiring matches a
// dependency to the one keeper that implements its interface, and an empty
// interface is implemented by every keeper, which stops the app from
// starting.
type TicketsKeeper interface {
	// PayFromProberFund pays a prober from the prober fund, up to what the
	// fund holds, and returns what was paid.
	PayFromProberFund(ctx context.Context, recipient sdk.AccAddress, want sdk.Coin) (sdk.Coin, error)
	// FundProberFund moves coins from a module account into the prober fund.
	FundProberFund(ctx context.Context, fromModule string, amount sdk.Coin) error
	// RedeemForFlag spends one ticket made out to a node and pays the node.
	RedeemForFlag(ctx context.Context, nodeID, keyID uint64, ticket string) error
}

// AuthKeeper defines the expected interface for the Auth module.
type AuthKeeper interface {
	AddressCodec() address.Codec
	GetAccount(context.Context, sdk.AccAddress) sdk.AccountI // only used for simulation
	// Methods imported from account should be defined here
}

// BankKeeper defines the expected interface for the Bank module.
type BankKeeper interface {
	SpendableCoins(context.Context, sdk.AccAddress) sdk.Coins
	// GetBalance reads what the node rewards pool holds.
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
	// SendCoinsFromAccountToModule locks a prober bond in the verify module
	// account.
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	// SendCoinsFromModuleToAccount returns a bond after unbonding.
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
}

// ParamSubspace defines the expected Subspace interface for parameters.
type ParamSubspace interface {
	Get(context.Context, []byte, interface{})
	Set(context.Context, []byte, interface{})
}
