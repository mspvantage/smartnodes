package keeper

import (
	"context"
	"strconv"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// RegisterNode creates a node and locks its bond in the registry module
// account. One operator may register several nodes, each with its own bond.
func (k msgServer) RegisterNode(ctx context.Context, msg *types.MsgRegisterNode) (*types.MsgRegisterNodeResponse, error) {
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}

	if err := types.ValidateTier(msg.Tier); err != nil {
		return nil, err
	}
	if err := types.ValidateEndpoint(msg.Endpoint); err != nil {
		return nil, err
	}
	if err := types.ValidatePubkey(msg.Pubkey); err != nil {
		return nil, err
	}
	if err := k.checkBondCoin(ctx, msg.Bond); err != nil {
		return nil, err
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	minBond, err := params.MinBond(msg.Tier)
	if err != nil {
		return nil, err
	}
	if msg.Bond.Amount.LT(math.NewIntFromUint64(minBond)) {
		return nil, errorsmod.Wrapf(types.ErrBondTooLow, "got %s, minimum %d%s", msg.Bond, minBond, msg.Bond.Denom)
	}

	taken, err := k.NodeByPubkey.Has(ctx, msg.Pubkey)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, types.ErrPubkeyTaken
	}

	// Lock the bond first. If the operator cannot pay, nothing is stored.
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, operator, types.ModuleName, sdk.NewCoins(msg.Bond)); err != nil {
		return nil, err
	}

	id, err := k.NodeSeq.Next(ctx)
	if err != nil {
		return nil, err
	}

	// Store the canonical text form of the address, whatever case was sent.
	operatorStr, err := k.addressCodec.BytesToString(operator)
	if err != nil {
		return nil, err
	}

	node := types.Node{
		Id:       id,
		Operator: operatorStr,
		Endpoint: msg.Endpoint,
		Pubkey:   msg.Pubkey,
		Tier:     msg.Tier,
		Bond:     msg.Bond,
		Status:   types.StatusActive,
	}
	if err := k.Node.Set(ctx, id, node); err != nil {
		return nil, err
	}
	if err := k.NodeByPubkey.Set(ctx, msg.Pubkey, id); err != nil {
		return nil, err
	}

	k.emit(ctx, EventTypeRegisterNode, id,
		sdk.NewAttribute(AttributeKeyOperator, operatorStr),
		sdk.NewAttribute(AttributeKeyTier, strconv.FormatUint(msg.Tier, 10)),
		sdk.NewAttribute(AttributeKeyAmount, msg.Bond.String()),
	)

	return &types.MsgRegisterNodeResponse{Id: id}, nil
}
