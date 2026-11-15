package verify

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/mspvantage/smartnodes/x/verify/types"
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
					RpcMethod: "ListProber",
					Use:       "list-prober",
					Short:     "List all prober",
				},
				{
					RpcMethod:      "GetProber",
					Use:            "get-prober [id]",
					Short:          "Gets a prober by id",
					Alias:          []string{"show-prober"},
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "id"}},
				},
				{
					RpcMethod: "Round",
					Use:       "round",
					Short:     "Shows the current verification round, its seed and when the next one starts",
				},
				{
					RpcMethod:      "NodeRecord",
					Use:            "node-record [node-id]",
					Short:          "Shows a node's verification record",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "node_id"}},
				},
				{
					RpcMethod:      "Assignment",
					Use:            "assignment [node-id]",
					Short:          "Shows whether a node is probed this round and which probers are assigned to it",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "node_id"}},
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
					RpcMethod:      "RegisterProber",
					Use:            "register-prober [bond]",
					Short:          "Send a register-prober tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "bond"}},
				},
				{
					RpcMethod:      "DeregisterProber",
					Use:            "deregister-prober ",
					Short:          "Send a deregister-prober tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{},
				},
				{
					RpcMethod:      "WithdrawProberBond",
					Use:            "withdraw-prober-bond ",
					Short:          "Send a withdraw-prober-bond tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{},
				},
				{
					RpcMethod:      "CommitReports",
					Use:            "commit-reports [round] [node-ids] [hashes]",
					Short:          "Post sealed reports for the current round: node ids as a comma-separated list, then one hash per node",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "round"}, {ProtoField: "node_ids"}, {ProtoField: "hashes", Varargs: true}},
				},
				{
					RpcMethod:      "RevealReport",
					Use:            "reveal-report [round] [node-id] [result] [speed-band] [salt]",
					Short:          "Open a sealed report from the previous round; result is 1 (pass), 2 (fail) or 3 (tamper)",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "round"}, {ProtoField: "node_id"}, {ProtoField: "result"}, {ProtoField: "speed_band"}, {ProtoField: "salt"}},
					FlagOptions:    map[string]*autocliv1.FlagOptions{"proof": {Usage: "proof of probe (not checked in this version)"}},
				},
				{
					RpcMethod:      "Unjail",
					Use:            "unjail [node-id]",
					Short:          "Send a unjail tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "node_id"}},
				},
			},
		},
	}
}
