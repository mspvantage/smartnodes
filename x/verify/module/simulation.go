package verify

import (
	"math/rand"

	"github.com/cosmos/cosmos-sdk/types/module"
	simtypes "github.com/cosmos/cosmos-sdk/types/simulation"
	"github.com/cosmos/cosmos-sdk/x/simulation"

	verifysimulation "github.com/mspvantage/smartnodes/x/verify/simulation"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

// GenerateGenesisState creates a randomized GenState of the module.
func (AppModule) GenerateGenesisState(simState *module.SimulationState) {
	accs := make([]string, len(simState.Accounts))
	for i, acc := range simState.Accounts {
		accs[i] = acc.Address.String()
	}
	verifyGenesis := types.GenesisState{
		Params: types.DefaultParams(),
	}
	simState.GenState[types.ModuleName] = simState.Cdc.MustMarshalJSON(&verifyGenesis)
}

// RegisterStoreDecoder registers a decoder.
func (am AppModule) RegisterStoreDecoder(_ simtypes.StoreDecoderRegistry) {}

// WeightedOperations returns the all the gov module operations with their respective weights.
func (am AppModule) WeightedOperations(simState module.SimulationState) []simtypes.WeightedOperation {
	operations := make([]simtypes.WeightedOperation, 0)
	const (
		opWeightMsgRegisterProber          = "op_weight_msg_verify"
		defaultWeightMsgRegisterProber int = 100
	)

	var weightMsgRegisterProber int
	simState.AppParams.GetOrGenerate(opWeightMsgRegisterProber, &weightMsgRegisterProber, nil,
		func(_ *rand.Rand) {
			weightMsgRegisterProber = defaultWeightMsgRegisterProber
		},
	)
	operations = append(operations, simulation.NewWeightedOperation(
		weightMsgRegisterProber,
		verifysimulation.SimulateMsgRegisterProber(am.authKeeper, am.bankKeeper, am.keeper, simState.TxConfig),
	))
	const (
		opWeightMsgDeregisterProber          = "op_weight_msg_verify"
		defaultWeightMsgDeregisterProber int = 100
	)

	var weightMsgDeregisterProber int
	simState.AppParams.GetOrGenerate(opWeightMsgDeregisterProber, &weightMsgDeregisterProber, nil,
		func(_ *rand.Rand) {
			weightMsgDeregisterProber = defaultWeightMsgDeregisterProber
		},
	)
	operations = append(operations, simulation.NewWeightedOperation(
		weightMsgDeregisterProber,
		verifysimulation.SimulateMsgDeregisterProber(am.authKeeper, am.bankKeeper, am.keeper, simState.TxConfig),
	))
	const (
		opWeightMsgWithdrawProberBond          = "op_weight_msg_verify"
		defaultWeightMsgWithdrawProberBond int = 100
	)

	var weightMsgWithdrawProberBond int
	simState.AppParams.GetOrGenerate(opWeightMsgWithdrawProberBond, &weightMsgWithdrawProberBond, nil,
		func(_ *rand.Rand) {
			weightMsgWithdrawProberBond = defaultWeightMsgWithdrawProberBond
		},
	)
	operations = append(operations, simulation.NewWeightedOperation(
		weightMsgWithdrawProberBond,
		verifysimulation.SimulateMsgWithdrawProberBond(am.authKeeper, am.bankKeeper, am.keeper, simState.TxConfig),
	))
	const (
		opWeightMsgCommitReports          = "op_weight_msg_verify"
		defaultWeightMsgCommitReports int = 100
	)

	var weightMsgCommitReports int
	simState.AppParams.GetOrGenerate(opWeightMsgCommitReports, &weightMsgCommitReports, nil,
		func(_ *rand.Rand) {
			weightMsgCommitReports = defaultWeightMsgCommitReports
		},
	)
	operations = append(operations, simulation.NewWeightedOperation(
		weightMsgCommitReports,
		verifysimulation.SimulateMsgCommitReports(am.authKeeper, am.bankKeeper, am.keeper, simState.TxConfig),
	))
	const (
		opWeightMsgRevealReport          = "op_weight_msg_verify"
		defaultWeightMsgRevealReport int = 100
	)

	var weightMsgRevealReport int
	simState.AppParams.GetOrGenerate(opWeightMsgRevealReport, &weightMsgRevealReport, nil,
		func(_ *rand.Rand) {
			weightMsgRevealReport = defaultWeightMsgRevealReport
		},
	)
	operations = append(operations, simulation.NewWeightedOperation(
		weightMsgRevealReport,
		verifysimulation.SimulateMsgRevealReport(am.authKeeper, am.bankKeeper, am.keeper, simState.TxConfig),
	))
	const (
		opWeightMsgUnjail          = "op_weight_msg_verify"
		defaultWeightMsgUnjail int = 100
	)

	var weightMsgUnjail int
	simState.AppParams.GetOrGenerate(opWeightMsgUnjail, &weightMsgUnjail, nil,
		func(_ *rand.Rand) {
			weightMsgUnjail = defaultWeightMsgUnjail
		},
	)
	operations = append(operations, simulation.NewWeightedOperation(
		weightMsgUnjail,
		verifysimulation.SimulateMsgUnjail(am.authKeeper, am.bankKeeper, am.keeper, simState.TxConfig),
	))

	return operations
}

// ProposalMsgs returns msgs used for governance proposals for simulations.
func (am AppModule) ProposalMsgs(simState module.SimulationState) []simtypes.WeightedProposalMsg {
	return []simtypes.WeightedProposalMsg{}
}
