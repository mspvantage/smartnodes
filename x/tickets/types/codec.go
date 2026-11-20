package types

import (
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

func RegisterInterfaces(registrar codectypes.InterfaceRegistry) {
	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgRedeemTickets{},
		&MsgRequestBridge{},
		&MsgAnswerBridge{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgFulfilPurchase{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgBuyTickets{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgAddIssuerKey{},
	)

	registrar.RegisterImplementations((*sdk.Msg)(nil),
		&MsgUpdateParams{},
	)
	msgservice.RegisterMsgServiceDesc(registrar, &_Msg_serviceDesc)
}
