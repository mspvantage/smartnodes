package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/tickets/types"
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
	IssuerKeySeq   collections.Sequence
	IssuerKey      collections.Map[uint64, types.IssuerKey]
	PurchaseSeq    collections.Sequence
	Purchase       collections.Map[uint64, types.Purchase]

	// Spent holds every redeemed ticket as (issuer key id, ticket key).
	Spent collections.KeySet[collections.Pair[uint64, []byte]]
	// Queue holds (block height, purchase id) entries: a refund deadline for
	// an unfulfilled purchase, or a deletion time for a fulfilled one.
	Queue collections.KeySet[collections.Pair[int64, uint64]]
	// ProberFund holds the verification module's share of protocol fees per
	// denom. The coins stay in the tickets module account until that module
	// exists to pay them out.
	ProberFund collections.Map[string, math.Int]
	// KeyExpiry queues issuer keys by redemption deadline (Unix time, key id).
	KeyExpiry collections.KeySet[collections.Pair[int64, uint64]]
	// SweptKeys holds issuer keys whose unspent value went to the community pool.
	SweptKeys collections.KeySet[uint64]
	// Clearing holds issuer keys whose spent-ticket records are being deleted.
	Clearing collections.KeySet[uint64]

	distrKeeper types.DistrKeeper
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,

	bankKeeper types.BankKeeper,
	registryKeeper types.RegistryKeeper,
	distrKeeper types.DistrKeeper,
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
		Params:         collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		IssuerKey:      collections.NewMap(sb, types.IssuerKeyKey, "issuerKey", collections.Uint64Key, codec.CollValue[types.IssuerKey](cdc)),
		IssuerKeySeq:   collections.NewSequence(sb, types.IssuerKeyCountKey, "issuerKeySequence"),
		Purchase:       collections.NewMap(sb, types.PurchaseKey, "purchase", collections.Uint64Key, codec.CollValue[types.Purchase](cdc)),
		PurchaseSeq:    collections.NewSequence(sb, types.PurchaseCountKey, "purchaseSequence"),
		Spent:          collections.NewKeySet(sb, types.SpentKey, "spent", collections.PairKeyCodec(collections.Uint64Key, collections.BytesKey)),
		Queue:          collections.NewKeySet(sb, types.QueueKey, "queue", collections.PairKeyCodec(collections.Int64Key, collections.Uint64Key)),
		ProberFund:     collections.NewMap(sb, types.ProberFundKey, "proberFund", collections.StringKey, sdk.IntValue),
		KeyExpiry:      collections.NewKeySet(sb, types.KeyExpiryKey, "keyExpiry", collections.PairKeyCodec(collections.Int64Key, collections.Uint64Key)),
		SweptKeys:      collections.NewKeySet(sb, types.SweptKey, "sweptKeys", collections.Uint64Key),
		Clearing:       collections.NewKeySet(sb, types.ClearingKey, "clearing", collections.Uint64Key),
		distrKeeper:    distrKeeper,
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
