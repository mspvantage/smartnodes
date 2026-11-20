package types

import (
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

func RegisterInterfaces(registrar codectypes.InterfaceRegistry) {
	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgSponsorNode{},
		&MsgFundSponsorPool{},
		&MsgSetServiceKey{},
		&MsgSetReality{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgWithdrawBond{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgDeregisterNode{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgAddBond{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgUpdateNode{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgRegisterNode{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgUpdateParams{},
	)
	msgservice.RegisterMsgServiceDesc(registrar, &_Msg_serviceDesc)
}
