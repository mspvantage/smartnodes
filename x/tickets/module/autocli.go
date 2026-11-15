package tickets

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// AutoCLIOptions implements the autocli.HasAutoCLIConfig interface.
func (am AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "Params",
					Use:       "params",
					Short:     "Shows the parameters of the module",
				},
				{
					RpcMethod: "ListIssuerKey",
					Use:       "list-issuer-key",
					Short:     "List all issuerKey",
				},
				{
					RpcMethod:      "GetIssuerKey",
					Use:            "get-issuer-key [id]",
					Short:          "Gets a issuerKey by id",
					Alias:          []string{"show-issuer-key"},
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{
					RpcMethod: "ListPurchase",
					Use:       "list-purchase",
					Short:     "List all purchase",
				},
				{
					RpcMethod:      "GetPurchase",
					Use:            "get-purchase [id]",
					Short:          "Gets a purchase by id",
					Alias:          []string{"show-purchase"},
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service:              types.Msg_serviceDesc.ServiceName,
			EnhanceCustomCommand: true, // only required if you want to use the custom command
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateParams",
					Skip:      true, // skipped because authority gated
				},
				{
					RpcMethod:      "AddIssuerKey",
					Use:            "add-issuer-key [pubkey] [price] [sign-end] [redeem-end]",
					Short:          "Send a add-issuer-key tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "pubkey"}, {ProtoField: "price"}, {ProtoField: "sign_end"}, {ProtoField: "redeem_end"}},
				},
				{
					RpcMethod:      "BuyTickets",
					Use:            "buy-tickets [key-id] [blinded]",
					Short:          "Send a buy-tickets tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "key_id"}, {ProtoField: "blinded", Varargs: true}},
				},
				{
					RpcMethod:      "FulfilPurchase",
					Use:            "fulfil-purchase [id] [signatures]",
					Short:          "Send a fulfil-purchase tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}, {ProtoField: "signatures", Varargs: true}},
				},
				{
					RpcMethod:      "RedeemTickets",
					Use:            "redeem-tickets [node-id] [key-id] [tickets]",
					Short:          "Send a redeem-tickets tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "node_id"}, {ProtoField: "key_id"}, {ProtoField: "tickets", Varargs: true}},
				},
			},
		},
	}
}
