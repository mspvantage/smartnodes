package types

import (
	"context"

	"cosmossdk.io/core/address"
	"cosmossdk.io/math"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// StakingKeeper defines the expected interface for the Staking module.
type StakingKeeper interface {
	ConsensusAddressCodec() address.Codec
	// ValidatorByConsAddr finds a validator from the address it signs blocks with.
	ValidatorByConsAddr(context.Context, sdk.ConsAddress) (stakingtypes.ValidatorI, error)
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
	// GetAllBalances reads what the fee collector holds.
	GetAllBalances(ctx context.Context, addr sdk.AccAddress) sdk.Coins
	// SendCoinsFromModuleToModule moves the equal share from the fee collector
	// to the distribution module, which pays validators and delegators.
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
}

// DistrKeeper defines what the rewards module needs from the distribution
// module.
type DistrKeeper interface {
	// GetCommunityTax returns the share of rewards that goes to the community pool.
	GetCommunityTax(ctx context.Context) (math.LegacyDec, error)
	// AllocateTokensToValidator credits rewards to a validator, split between
	// its commission and its delegators.
	AllocateTokensToValidator(ctx context.Context, val stakingtypes.ValidatorI, tokens sdk.DecCoins) error
	// FundCommunityPool moves coins from sender into the community pool.
	FundCommunityPool(ctx context.Context, amount sdk.Coins, sender sdk.AccAddress) error
}

// ParamSubspace defines the expected Subspace interface for parameters.
type ParamSubspace interface {
	Get(context.Context, []byte, interface{})
	Set(context.Context, []byte, interface{})
}
