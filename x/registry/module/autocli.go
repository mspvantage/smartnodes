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
					RpcMethod:      "SponsorNode",
					Use:            "sponsor-node [operator] [endpoint] [pubkey] [tier]",
					Short:          "Register a node for an operator with a bond lent by the sponsor pool; only the sponsor account may",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator"}, {ProtoField: "endpoint"}, {ProtoField: "pubkey"}, {ProtoField: "tier"}},
				},
				{
					RpcMethod:      "FundSponsorPool",
					Use:            "fund-sponsor-pool [amount]",
					Short:          "Add coins to the sponsor pool; the amount is in the base unit of the staking token",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "amount"}},
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
					RpcMethod:      "SetServiceKey",
					Use:            "set-service-key [id] [service-key]",
					Short:          "Let a second account redeem this node's tickets (payment still goes to the operator); pass \"\" to clear it",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}, {ProtoField: "service_key"}},
				},
				{
					RpcMethod:      "SetReality",
					Use:            "set-reality [id] [port] [public-key] [short-id] [server-name] [uuid]",
					Short:          "Publish an entry node's REALITY disguise (smartnode prints this command); port 0 with empty values removes it",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}, {ProtoField: "port"}, {ProtoField: "public_key"}, {ProtoField: "short_id"}, {ProtoField: "server_name"}, {ProtoField: "uuid"}},
				},
				{
					RpcMethod:      "RegisterBridge",
					Use:            "register-bridge [commitment] [bond]",
					Short:          "Register an unlisted entry node, a bridge, by its bridge line's fingerprint ('smartnode bridge-line' prints it)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "commitment"}, {ProtoField: "bond"}},
				},
				{
					RpcMethod:      "UpdateBridge",
					Use:            "update-bridge [id] [commitment]",
					Short:          "Replace a bridge's fingerprint, as after moving it to a new address",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}, {ProtoField: "commitment"}},
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
