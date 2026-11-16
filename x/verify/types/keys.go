package types

import "cosmossdk.io/collections"

const (
	// ModuleName defines the module name
	ModuleName = "verify"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// RewardsPoolName is the module account that holds the node rewards
	// pool. It is separate from the account that holds prober bonds, so each
	// can be checked against its own records.
	RewardsPoolName = "verify_node_rewards"

	// GovModuleName duplicates the gov module's name to avoid a dependency with x/gov.
	// It should be synced with the gov module's name if it is ever changed.
	// See: https://github.com/cosmos/cosmos-sdk/blob/v0.52.0-beta.2/x/gov/types/keys.go#L9
	GovModuleName = "gov"
)

// ParamsKey is the prefix to retrieve all Params
var ParamsKey = collections.NewPrefix("p_verify")

var (
	ProberKey      = collections.NewPrefix("prober/value/")
	ProberCountKey = collections.NewPrefix("prober/count/")
)

// Prefixes of the collections added by hand. No prefix may be the start of
// another.
var (
	// ProberByOwnerKey maps an owner address to its prober id.
	ProberByOwnerKey = collections.NewPrefix("owner/")
	// ActiveListKey maps a position in the active list to a prober id.
	ActiveListKey = collections.NewPrefix("active/list/")
	// ActiveIndexKey maps a prober id to its position in the active list.
	ActiveIndexKey = collections.NewPrefix("active/index/")
	// ActiveCountKey holds the length of the active list.
	ActiveCountKey = collections.NewPrefix("active/count/")
	// PendingJoinKey holds probers waiting to join at the next round.
	PendingJoinKey = collections.NewPrefix("pending/join/")
	// PendingLeaveKey holds probers waiting to leave at the next round.
	PendingLeaveKey = collections.NewPrefix("pending/leave/")
	// RoundNumberKey holds the current round number.
	RoundNumberKey = collections.NewPrefix("round/number/")
	// RoundStartKey holds the block height at which the current round began.
	RoundStartKey = collections.NewPrefix("round/start/")
	// RoundSeedKey maps a round number to its seed.
	RoundSeedKey = collections.NewPrefix("round/seed/")
	// ReportKey holds sealed and opened reports, ordered by round, then node,
	// then prober.
	ReportKey = collections.NewPrefix("report/")
	// NodeRecordKey maps a node id to its verification record.
	NodeRecordKey = collections.NewPrefix("nodeRecord/")
	// RoundBudgetKey maps a round to its reward budget for verified nodes.
	RoundBudgetKey = collections.NewPrefix("reward/budget/")
	// RoundWeightKey maps a round to the total weight of the nodes that
	// earned a share of its budget.
	RoundWeightKey = collections.NewPrefix("reward/weight/")
	// RewardOwedKey holds the total of all rewards earned and not yet
	// claimed.
	RewardOwedKey = collections.NewPrefix("reward/owed/")
)
