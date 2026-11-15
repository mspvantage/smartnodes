package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// Unjail will return a jailed node to service. This version records results
// only and never jails, so there is nothing to undo and the message is
// refused. Jailing arrives in stage 2 of the verification module.
func (k msgServer) Unjail(_ context.Context, msg *types.MsgUnjail) (*types.MsgUnjailResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Operator); err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}
	return nil, types.ErrJailingNotAvailable
}
