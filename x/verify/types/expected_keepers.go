package types

import (
	"context"

	"cosmossdk.io/core/address"

	sdk "github.com/cosmos/cosmos-sdk/types"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
)

// RegistryKeeper defines what the verify module needs from the node registry.
type RegistryKeeper interface {
	// GetNode returns a registered node, or an error if it does not exist.
	GetNode(ctx context.Context, id uint64) (registrytypes.Node, error)
	// BondDenom returns the staking token denom. Prober bonds must use it.
	BondDenom(ctx context.Context) (string, error)
}

// TicketsKeeper defines what the verify module needs from the tickets module.
//
// The proof-of-probe check that will use the tickets module arrives in
// stage 2. Until then this interface names one method the tickets keeper
// already has. It must not be left empty: the app wiring matches a
// dependency to the one keeper that implements its interface, and an empty
// interface is implemented by every keeper, which stops the app from
// starting.
type TicketsKeeper interface {
	// EscrowExpected returns what the ticket escrow should hold.
	EscrowExpected(ctx context.Context) (sdk.Coins, error)
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
