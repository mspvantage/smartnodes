package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

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
