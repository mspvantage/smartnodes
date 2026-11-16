package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	// Address capable of executing a MsgUpdateParams message.
	// Typically, this should be the x/gov module account.
	authority []byte

	Schema collections.Schema
	Params collections.Item[types.Params]

	bankKeeper     types.BankKeeper
	registryKeeper types.RegistryKeeper
	ticketsKeeper  types.TicketsKeeper
	ProberSeq      collections.Sequence
	Prober         collections.Map[uint64, types.Prober]

	// ProberByOwner maps an owner address to its prober id. One address may
	// run one prober.
	ProberByOwner collections.Map[string, uint64]

	// The active set is kept as a list so that a prober can be drawn by
	// position at a fixed cost. ActiveList maps position to prober id,
	// ActiveIndex maps prober id to position, ActiveCount is the length.
	// The list changes only when a round starts.
	ActiveList  collections.Map[uint64, uint64]
	ActiveIndex collections.Map[uint64, uint64]
	ActiveCount collections.Item[uint64]
	// PendingJoin and PendingLeave hold the probers that enter or leave the
	// active set when the next round starts.
	PendingJoin  collections.KeySet[uint64]
	PendingLeave collections.KeySet[uint64]

	// RoundNumber is the current round, RoundStart the block it began at,
	// and RoundSeed the random seed of each recent round.
	RoundNumber collections.Item[uint64]
	RoundStart  collections.Item[int64]
	RoundSeed   collections.Map[uint64, []byte]

	// Reports holds sealed and opened reports until their round is tallied.
	// The key is (round, node id then prober id as 16 bytes), so a round's
	// reports are stored together and a node's reports sit side by side.
	Reports collections.Map[collections.Pair[uint64, []byte], []byte]

	// NodeRecord maps a node id to its verification record.
	NodeRecord collections.Map[uint64, types.NodeRecord]

	// RoundBudget is each recent round's reward budget for verified nodes,
	// RoundWeight the total weight of the nodes that earned a share of it,
	// and RewardOwed the total earned by nodes and not yet claimed. The
	// node rewards pool account must always hold at least RewardOwed.
	RoundBudget collections.Map[uint64, uint64]
	RoundWeight collections.Map[uint64, uint64]
	RewardOwed  collections.Item[uint64]
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,

	bankKeeper types.BankKeeper,
	registryKeeper types.RegistryKeeper,
	ticketsKeeper types.TicketsKeeper,
) Keeper {
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid authority address %s: %s", authority, err))
	}

	sb := collections.NewSchemaBuilder(storeService)

	k := Keeper{
		storeService: storeService,
		cdc:          cdc,
		addressCodec: addressCodec,
		authority:    authority,

		bankKeeper:     bankKeeper,
		registryKeeper: registryKeeper,
		ticketsKeeper:  ticketsKeeper,
		Params:         collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Prober:         collections.NewMap(sb, types.ProberKey, "prober", collections.Uint64Key, codec.CollValue[types.Prober](cdc)),
		ProberSeq:      collections.NewSequence(sb, types.ProberCountKey, "proberSequence"),

		ProberByOwner: collections.NewMap(sb, types.ProberByOwnerKey, "proberByOwner", collections.StringKey, collections.Uint64Value),
		ActiveList:    collections.NewMap(sb, types.ActiveListKey, "activeList", collections.Uint64Key, collections.Uint64Value),
		ActiveIndex:   collections.NewMap(sb, types.ActiveIndexKey, "activeIndex", collections.Uint64Key, collections.Uint64Value),
		ActiveCount:   collections.NewItem(sb, types.ActiveCountKey, "activeCount", collections.Uint64Value),
		PendingJoin:   collections.NewKeySet(sb, types.PendingJoinKey, "pendingJoin", collections.Uint64Key),
		PendingLeave:  collections.NewKeySet(sb, types.PendingLeaveKey, "pendingLeave", collections.Uint64Key),
		RoundNumber:   collections.NewItem(sb, types.RoundNumberKey, "roundNumber", collections.Uint64Value),
		RoundStart:    collections.NewItem(sb, types.RoundStartKey, "roundStart", collections.Int64Value),
		RoundSeed:     collections.NewMap(sb, types.RoundSeedKey, "roundSeed", collections.Uint64Key, collections.BytesValue),
		Reports:       collections.NewMap(sb, types.ReportKey, "reports", collections.PairKeyCodec(collections.Uint64Key, collections.BytesKey), collections.BytesValue),
		NodeRecord:    collections.NewMap(sb, types.NodeRecordKey, "nodeRecord", collections.Uint64Key, codec.CollValue[types.NodeRecord](cdc)),
		RoundBudget:   collections.NewMap(sb, types.RoundBudgetKey, "roundBudget", collections.Uint64Key, collections.Uint64Value),
		RoundWeight:   collections.NewMap(sb, types.RoundWeightKey, "roundWeight", collections.Uint64Key, collections.Uint64Value),
		RewardOwed:    collections.NewItem(sb, types.RewardOwedKey, "rewardOwed", collections.Uint64Value),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	return k
}

// GetAuthority returns the module's authority.
func (k Keeper) GetAuthority() []byte {
	return k.authority
}
