package types

import (
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

func RegisterInterfaces(registrar codectypes.InterfaceRegistry) {
	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgUnjail{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgRevealReport{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgCommitReports{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgWithdrawProberBond{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgDeregisterProber{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgRegisterProber{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgUpdateParams{},
	)
	msgservice.RegisterMsgServiceDesc(registrar, &_Msg_serviceDesc)
}
