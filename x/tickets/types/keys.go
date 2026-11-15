package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name
	ModuleName = "tickets"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	// It should be synced with the gov module's name if it is ever changed.
	// See: https://github.com/cosmos/cosmos-sdk/blob/v0.52.0-beta.2/x/gov/types/keys.go#L9
	GovModuleName = "gov"
)

// ParamsKey is the prefix to retrieve all Params
var ParamsKey = collections.NewPrefix("p_tickets")

var (
	IssuerKeyKey      = collections.NewPrefix("issuerkey/value/")
	IssuerKeyCountKey = collections.NewPrefix("issuerkey/count/")
)

var (
	PurchaseKey      = collections.NewPrefix("purchase/value/")
	PurchaseCountKey = collections.NewPrefix("purchase/count/")
)

var (
	// SpentKey prefixes the set of redeemed tickets, keyed by issuer key id
	// and ticket public key.
	SpentKey = collections.NewPrefix("spent/")
	// QueueKey prefixes the work queue processed at the end of each block,
	// keyed by block height and purchase id.
	QueueKey = collections.NewPrefix("queue/")
	// AccruedFeesKey prefixes protocol fees collected and not yet distributed,
	// keyed by denom.
	AccruedFeesKey = collections.NewPrefix("fees/")
)
