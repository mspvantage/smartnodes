package keeper

import (
	"bytes"
	"context"
	"strconv"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// SponsorNode registers a node for an operator with a bond lent by the
// sponsor pool, so an early operator can start without buying tokens first.
//
// Only the sponsor account named in the module's settings may send it. The
// sponsor decides who is worth sponsoring; the chain enforces the rest:
//
//   - the bond is the tier's minimum, taken from the sponsor pool;
//   - it is locked and slashable like any bond;
//   - it returns to the pool, not to the operator, when the node leaves;
//   - no more than max_sponsored_nodes may be sponsored at once.
func (k msgServer) SponsorNode(ctx context.Context, msg *types.MsgSponsorNode) (*types.MsgSponsorNodeResponse, error) {
	signer, err := k.addressCodec.StringToBytes(msg.Sponsor)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid sponsor address")
	}
	operator, err := k.addressCodec.StringToBytes(msg.Operator)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid operator address")
	}

	sponsor, err := k.sponsorAddress(ctx)
	if err != nil {
		return nil, err
	}
	if sponsor == nil {
		return nil, types.ErrSponsorshipOff
	}
	if !bytes.Equal(sponsor, signer) {
		return nil, types.ErrNotSponsor
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
	taken, err := k.NodeByPubkey.Has(ctx, msg.Pubkey)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, types.ErrPubkeyTaken
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	count, err := k.sponsoredCount(ctx)
	if err != nil {
		return nil, err
	}
	if count >= params.MaxSponsoredNodes {
		return nil, errorsmod.Wrapf(types.ErrSponsorLimit, "%d of %d", count, params.MaxSponsoredNodes)
	}

	minBond, err := params.MinBond(msg.Tier)
	if err != nil {
		return nil, err
	}
	denom, err := k.stakingKeeper.BondDenom(ctx)
	if err != nil {
		return nil, err
	}
	bond := sdk.NewCoin(denom, math.NewIntFromUint64(minBond))

	// Lend the bond first. If the pool cannot cover it, nothing is stored.
	if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.SponsorPoolName, types.ModuleName, sdk.NewCoins(bond)); err != nil {
		return nil, err
	}

	id, err := k.NodeSeq.Next(ctx)
	if err != nil {
		return nil, err
	}
	operatorStr, err := k.addressCodec.BytesToString(operator)
	if err != nil {
		return nil, err
	}
	node := types.Node{
		Id:        id,
		Operator:  operatorStr,
		Endpoint:  msg.Endpoint,
		Pubkey:    msg.Pubkey,
		Tier:      msg.Tier,
		Bond:      bond,
		Status:    types.StatusActive,
		Sponsored: minBond,
	}
	if err := k.Node.Set(ctx, id, node); err != nil {
		return nil, err
	}
	if err := k.NodeByPubkey.Set(ctx, msg.Pubkey, id); err != nil {
		return nil, err
	}
	if err := k.addSponsoredCount(ctx, 1); err != nil {
		return nil, err
	}

	k.emit(ctx, EventTypeSponsorNode, id,
		sdk.NewAttribute(AttributeKeyOperator, operatorStr),
		sdk.NewAttribute(AttributeKeyTier, strconv.FormatUint(msg.Tier, 10)),
		sdk.NewAttribute(AttributeKeySponsored, bond.String()),
	)

	return &types.MsgSponsorNodeResponse{Id: id}, nil
}

// FundSponsorPool adds the sender's coins to the sponsor pool. Anyone may
// fund it; coins leave it only as sponsored bonds, and come back when the
// sponsored node leaves.
func (k msgServer) FundSponsorPool(ctx context.Context, msg *types.MsgFundSponsorPool) (*types.MsgFundSponsorPoolResponse, error) {
	sender, err := k.addressCodec.StringToBytes(msg.Sender)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid sender address")
	}
	if msg.Amount == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidAmount, "amount must be greater than zero")
	}
	denom, err := k.stakingKeeper.BondDenom(ctx)
	if err != nil {
		return nil, err
	}
	coins := sdk.NewCoins(sdk.NewCoin(denom, math.NewIntFromUint64(msg.Amount)))
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, sender, types.SponsorPoolName, coins); err != nil {
		return nil, err
	}

	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(EventTypeFundSponsorPool,
		sdk.NewAttribute(AttributeKeyAmount, coins.String()),
	))

	return &types.MsgFundSponsorPoolResponse{}, nil
}
