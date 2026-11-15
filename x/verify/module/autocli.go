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
					Short:          "Send a commit-reports tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "round"}, {ProtoField: "node_ids"}, {ProtoField: "hashes", Varargs: true}},
				},
				{
					RpcMethod:      "RevealReport",
					Use:            "reveal-report [round] [node-id] [result] [speed-band] [salt] [proof]",
					Short:          "Send a reveal-report tx",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "round"}, {ProtoField: "node_id"}, {ProtoField: "result"}, {ProtoField: "speed_band"}, {ProtoField: "salt"}, {ProtoField: "proof"}},
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
