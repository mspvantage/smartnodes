package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name
	ModuleName = "registry"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// SponsorPoolName is the module account that holds coins set aside to
	// lend as node bonds. It is separate from the account that holds the
	// bonds themselves.
	SponsorPoolName = "registry_sponsor_pool"

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	// It should be synced with the gov module's name if it is ever changed.
	// See: https://github.com/cosmos/cosmos-sdk/blob/v0.52.0-beta.2/x/gov/types/keys.go#L9
	GovModuleName = "gov"
)

// ParamsKey is the prefix to retrieve all Params
var ParamsKey = collections.NewPrefix("p_registry")

var (
	NodeKey      = collections.NewPrefix("node/value/")
	NodeCountKey = collections.NewPrefix("node/count/")
	// NodeByPubkeyKey indexes node ids by public key, so a key can be
	// registered only once.
	NodeByPubkeyKey = collections.NewPrefix("node/pubkey/")
	// SponsoredCountKey holds how many nodes currently have a sponsored bond.
	SponsoredCountKey = collections.NewPrefix("node/sponsored/")
)
