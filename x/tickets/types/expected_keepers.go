package types

import (
	"context"

	"cosmossdk.io/core/address"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
)

// RegistryKeeper defines what the tickets module needs from the node registry.
type RegistryKeeper interface {
	// GetNode returns a registered node, or an error if it does not exist.
	GetNode(ctx context.Context, id uint64) (registrytypes.Node, error)
	// WalkNodes calls fn for every node, in id order, until fn returns true.
	WalkNodes(ctx context.Context, fn func(registrytypes.Node) (stop bool, err error)) error
	// SlashNode burns up to amount from a node's bond and returns what was
	// burned.
	SlashNode(ctx context.Context, id uint64, amount math.Int) (math.Int, error)
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
	// SendCoinsFromAccountToModule locks a buyer's payment in escrow.
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	// SendCoinsFromModuleToAccount pays a node, or refunds a buyer.
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	// SendCoinsFromModuleToModule passes the stakers' share of the protocol
	// fee to the fee collector.
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
}

// DistrKeeper defines what the tickets module needs from the distribution
// module.
type DistrKeeper interface {
	// FundCommunityPool moves coins from sender into the community pool.
	FundCommunityPool(ctx context.Context, amount sdk.Coins, sender sdk.AccAddress) error
}

// ParamSubspace defines the expected Subspace interface for parameters.
type ParamSubspace interface {
	Get(context.Context, []byte, interface{})
	Set(context.Context, []byte, interface{})
}
