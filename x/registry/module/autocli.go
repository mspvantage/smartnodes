package registry

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/mspvantage/smartnodes/x/registry/types"
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
					RpcMethod: "ListNode",
					Use:       "list-node",
					Short:     "List all node",
				},
				{
					RpcMethod:      "GetNode",
					Use:            "get-node [id]",
					Short:          "Gets a node by id",
					Alias:          []string{"show-node"},
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
					RpcMethod:      "RegisterNode",
					Use:            "register-node [endpoint] [pubkey] [tier] [bond]",
					Short:          "Send a register-node tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "endpoint"}, {ProtoField: "pubkey"}, {ProtoField: "tier"}, {ProtoField: "bond"}},
				},
				{
					RpcMethod:      "UpdateNode",
					Use:            "update-node [id] [endpoint] [pubkey]",
					Short:          "Send a update-node tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}, {ProtoField: "endpoint"}, {ProtoField: "pubkey"}},
				},
				{
					RpcMethod:      "AddBond",
					Use:            "add-bond [id] [amount]",
					Short:          "Send a add-bond tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}, {ProtoField: "amount"}},
				},
				{
					RpcMethod:      "DeregisterNode",
					Use:            "deregister-node [id]",
					Short:          "Send a deregister-node tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{
					RpcMethod:      "WithdrawBond",
					Use:            "withdraw-bond [id]",
					Short:          "Send a withdraw-bond tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
			},
		},
	}
}
