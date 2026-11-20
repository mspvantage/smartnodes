package keeper

import (
	"bytes"
	"context"
	"errors"
	"strconv"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

// Event types and attribute keys emitted by the registry module.
const (
	EventTypeRegisterNode   = "registry_register_node"
	EventTypeUpdateNode     = "registry_update_node"
	EventTypeAddBond        = "registry_add_bond"
	EventTypeDeregisterNode = "registry_deregister_node"
	EventTypeWithdrawBond   = "registry_withdraw_bond"
	EventTypeSlashNode      = "registry_slash_node"

	AttributeKeyNodeID         = "node_id"
	AttributeKeyOperator       = "operator"
	AttributeKeyTier           = "tier"
	AttributeKeyAmount         = "amount"
	AttributeKeyUnbondingEnd   = "unbonding_end"
	AttributeKeyReturnedToPool = "returned_to_sponsor_pool"
	AttributeKeySponsored      = "sponsored"

	EventTypeSetServiceKey   = "registry_set_service_key"
	EventTypeSetReality      = "registry_set_reality"
	AttributeKeyRealityPort  = "reality_port"
	AttributeKeyServiceKey   = "service_key"
	EventTypeSponsorNode     = "registry_sponsor_node"
	EventTypeFundSponsorPool = "registry_fund_sponsor_pool"
)

// blockUnix returns the current block time as Unix seconds. Block time comes
// from consensus, so every validator sees the same value.
func (k Keeper) blockUnix(ctx context.Context) int64 {
	return sdk.UnwrapSDKContext(ctx).BlockTime().Unix()
}

// emit records an event for the given node.
func (k Keeper) emit(ctx context.Context, eventType string, nodeID uint64, attrs ...sdk.Attribute) {
	all := append([]sdk.Attribute{sdk.NewAttribute(AttributeKeyNodeID, strconv.FormatUint(nodeID, 10))}, attrs...)
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(eventType, all...))
}

// GetNode returns a node by id, or ErrNodeNotFound.
func (k Keeper) GetNode(ctx context.Context, id uint64) (types.Node, error) {
	node, err := k.Node.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Node{}, errorsmod.Wrapf(types.ErrNodeNotFound, "id %d", id)
		}
		return types.Node{}, err
	}
	return node, nil
}

// ownedNode loads a node and checks that operator (address bytes) owns it.
func (k Keeper) ownedNode(ctx context.Context, id uint64, operator []byte) (types.Node, error) {
	node, err := k.GetNode(ctx, id)
	if err != nil {
		return types.Node{}, err
	}
	owner, err := k.addressCodec.StringToBytes(node.Operator)
	if err != nil {
		return types.Node{}, errorsmod.Wrap(types.ErrInvalidNode, "stored operator address is invalid")
	}
	if !bytes.Equal(owner, operator) {
		return types.Node{}, errorsmod.Wrapf(types.ErrNotOperator, "node %d", id)
	}
	return node, nil
}

// checkBondCoin verifies that coin is a positive amount of the staking token.
func (k Keeper) checkBondCoin(ctx context.Context, coin sdk.Coin) error {
	if err := coin.Validate(); err != nil {
		return errorsmod.Wrap(types.ErrInvalidBond, err.Error())
	}
	if !coin.IsPositive() {
		return errorsmod.Wrap(types.ErrInvalidBond, "amount must be greater than zero")
	}
	denom, err := k.stakingKeeper.BondDenom(ctx)
	if err != nil {
		return err
	}
	if coin.Denom != denom {
		return errorsmod.Wrapf(types.ErrInvalidBond, "denom %s, want %s", coin.Denom, denom)
	}
	return nil
}

// BondDenom returns the staking token denom, the only coin accepted as a
// bond. Other modules that take bonds use it so that all bonds are in the
// same token.
func (k Keeper) BondDenom(ctx context.Context) (string, error) {
	return k.stakingKeeper.BondDenom(ctx)
}

// MinBondFor returns the minimum bond of a tier. The verification module
// sizes a node's reward cap from it.
func (k Keeper) MinBondFor(ctx context.Context, tier uint64) (uint64, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	return params.MinBond(tier)
}

// TotalBonded returns the sum of all node bonds. The registry module account
// must always hold exactly this amount of the staking token.
func (k Keeper) TotalBonded(ctx context.Context) (math.Int, error) {
	total := math.ZeroInt()
	err := k.Node.Walk(ctx, nil, func(_ uint64, node types.Node) (bool, error) {
		total = total.Add(node.Bond.Amount)
		return false, nil
	})
	return total, err
}

// SlashNode burns up to amount from a node's bond and returns what was
// actually burned. It works on active and unbonding nodes alike, which is why
// bonds stay locked during unbonding.
//
// No message calls this yet. It is the hook the verification module will use
// once independent probers can prove a fault.
func (k Keeper) SlashNode(ctx context.Context, id uint64, amount math.Int) (math.Int, error) {
	if amount.IsNil() || !amount.IsPositive() {
		return math.ZeroInt(), errorsmod.Wrap(types.ErrInvalidSlashAmount, "amount must be greater than zero")
	}
	node, err := k.GetNode(ctx, id)
	if err != nil {
		return math.ZeroInt(), err
	}

	burn := math.MinInt(amount, node.Bond.Amount)
	if burn.IsZero() {
		return burn, nil
	}

	burnCoin := sdk.NewCoin(node.Bond.Denom, burn)
	if err := k.bankKeeper.BurnCoins(ctx, types.ModuleName, sdk.NewCoins(burnCoin)); err != nil {
		return math.ZeroInt(), err
	}
	node.Bond = node.Bond.Sub(burnCoin)
	// A slash burns the operator's own part of the bond first. Only once
	// that is gone does it eat into the sponsored part.
	if left := node.Bond.Amount; math.NewIntFromUint64(node.Sponsored).GT(left) {
		hadSponsorship := node.Sponsored > 0
		node.Sponsored = left.Uint64()
		if hadSponsorship && node.Sponsored == 0 {
			if err := k.addSponsoredCount(ctx, -1); err != nil {
				return math.ZeroInt(), err
			}
		}
	}
	if err := k.Node.Set(ctx, id, node); err != nil {
		return math.ZeroInt(), err
	}

	k.emit(ctx, EventTypeSlashNode, id, sdk.NewAttribute(AttributeKeyAmount, burnCoin.String()))
	return burn, nil
}

// sponsoredCount returns how many nodes currently hold a sponsored bond.
func (k Keeper) sponsoredCount(ctx context.Context) (uint64, error) {
	n, err := k.SponsoredCount.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return n, nil
}

// addSponsoredCount moves the count up or down by one, never below zero.
func (k Keeper) addSponsoredCount(ctx context.Context, delta int) error {
	n, err := k.sponsoredCount(ctx)
	if err != nil {
		return err
	}
	switch {
	case delta > 0:
		n++
	case delta < 0 && n > 0:
		n--
	}
	return k.SponsoredCount.Set(ctx, n)
}

// sponsorAddress returns the sponsor account, or nil if sponsorship is off.
func (k Keeper) sponsorAddress(ctx context.Context) ([]byte, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if params.Sponsor == "" {
		return nil, nil
	}
	return k.addressCodec.StringToBytes(params.Sponsor)
}

// controlledNode loads a node the signer may take out of service: its
// operator always may, and the sponsor account may if the node still holds a
// sponsored bond. The sponsor needs this to recover a bond from a node whose
// operator has walked away. The bond itself always goes back to where it
// came from, whoever asks.
func (k Keeper) controlledNode(ctx context.Context, id uint64, signer []byte) (types.Node, error) {
	node, err := k.GetNode(ctx, id)
	if err != nil {
		return types.Node{}, err
	}
	owner, err := k.addressCodec.StringToBytes(node.Operator)
	if err != nil {
		return types.Node{}, errorsmod.Wrap(types.ErrInvalidNode, "stored operator address is invalid")
	}
	if bytes.Equal(owner, signer) {
		return node, nil
	}
	if node.Sponsored > 0 {
		sponsor, err := k.sponsorAddress(ctx)
		if err != nil {
			return types.Node{}, err
		}
		if sponsor != nil && bytes.Equal(sponsor, signer) {
			return node, nil
		}
	}
	return types.Node{}, errorsmod.Wrapf(types.ErrNotOperator, "node %d", id)
}

// WalkNodes calls fn for every registered node, in id order, until fn
// returns true or an error.
func (k Keeper) WalkNodes(ctx context.Context, fn func(types.Node) (bool, error)) error {
	return k.Node.Walk(ctx, nil, func(_ uint64, n types.Node) (bool, error) {
		return fn(n)
	})
}
