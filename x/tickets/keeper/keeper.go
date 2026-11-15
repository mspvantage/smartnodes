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
	// AccruedFees holds protocol fees per denom, kept in the module account
	// until the distribution rules are built.
	AccruedFees collections.Map[string, math.Int]
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,

	bankKeeper types.BankKeeper,
	registryKeeper types.RegistryKeeper,
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
		AccruedFees:    collections.NewMap(sb, types.AccruedFeesKey, "accruedFees", collections.StringKey, sdk.IntValue),
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
