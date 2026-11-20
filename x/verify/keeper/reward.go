package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

// The availability reward
//
// A pool account pays nodes for passing verification. It never pays in
// proportion to ticket volume, because a volume reward can be farmed by an
// operator spending tickets at their own node.
//
// The chain never loops over all nodes to pay them:
//
//  1. When a round starts, it gets a budget: a small share of what the pool
//     holds beyond what it already owes.
//  2. When a node passes a round, the tally notes the node as an earner of
//     that round and adds its weight to the round's total.
//  3. The next time the node is touched (its next tally, or a claim) the
//     share is fixed: budget x weight / total weight, never above the cap.
//     It is added to the node's balance, which never expires.
//  4. The operator claims the balance whenever they like.
//
// Every step costs the same however many nodes exist.

// PoolAddress returns the address of the node rewards pool account.
func PoolAddress() sdk.AccAddress {
	return authtypes.NewModuleAddress(types.RewardsPoolName)
}

// rewardOwed returns the total earned by nodes and not yet claimed.
func (k Keeper) rewardOwed(ctx context.Context) (uint64, error) {
	owed, err := k.RewardOwed.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return owed, nil
}

// PoolState returns what the pool account holds, what it owes, and the
// difference, which is what future rounds can still give out.
func (k Keeper) PoolState(ctx context.Context) (balance, owed, free math.Int, denom string, err error) {
	denom, err = k.registryKeeper.BondDenom(ctx)
	if err != nil {
		return math.ZeroInt(), math.ZeroInt(), math.ZeroInt(), "", err
	}
	owedRaw, err := k.rewardOwed(ctx)
	if err != nil {
		return math.ZeroInt(), math.ZeroInt(), math.ZeroInt(), "", err
	}
	balance = k.bankKeeper.GetBalance(ctx, PoolAddress(), denom).Amount
	owed = math.NewIntFromUint64(owedRaw)
	free = balance.Sub(owed)
	if free.IsNegative() {
		free = math.ZeroInt()
	}
	return balance, owed, free, denom, nil
}

// openRewardRound sets the budget of a new round and deletes the budget of
// the round that has aged out.
//
// The release for a round is: free balance x yearly release rate x the
// round's share of a year. Because it is a share of what is left, the pool
// shrinks smoothly and never runs dry in one step. The prober fund's part
// moves at once; the nodes' part stays in the pool until it is earned.
func (k Keeper) openRewardRound(ctx context.Context, round uint64, params types.Params) error {
	if round > params.RewardRetentionRounds {
		old := round - params.RewardRetentionRounds
		if err := k.RoundBudget.Remove(ctx, old); err != nil {
			return err
		}
		if err := k.RoundWeight.Remove(ctx, old); err != nil {
			return err
		}
	}

	_, _, free, denom, err := k.PoolState(ctx)
	if err != nil {
		return err
	}
	if !free.IsPositive() || params.BlocksPerYear == 0 {
		return nil
	}
	release := free.
		Mul(math.NewIntFromUint64(params.PoolReleaseBpsPerYear)).
		Mul(math.NewIntFromUint64(params.RoundBlocks)).
		Quo(math.NewIntFromUint64(types.BpsDenominator)).
		Quo(math.NewIntFromUint64(params.BlocksPerYear))
	if !release.IsPositive() {
		return nil
	}
	// A round can never give out more than the pool has free.
	release = math.MinInt(release, free)

	nodeBudget := release.Mul(math.NewIntFromUint64(params.NodeShareBps)).Quo(math.NewIntFromUint64(types.BpsDenominator))
	proberPart := release.Sub(nodeBudget)

	if proberPart.IsPositive() && k.ticketsKeeper != nil {
		if err := k.ticketsKeeper.FundProberFund(ctx, types.RewardsPoolName, sdk.NewCoin(denom, proberPart)); err != nil {
			return err
		}
	}
	if nodeBudget.IsPositive() && nodeBudget.IsUint64() {
		if err := k.RoundBudget.Set(ctx, round, nodeBudget.Uint64()); err != nil {
			return err
		}
	}

	emit(ctx, EventTypeRewardBudget,
		sdk.NewAttribute(AttributeKeyRound, u64(round)),
		sdk.NewAttribute(AttributeKeyNodes, nodeBudget.String()),
		sdk.NewAttribute(AttributeKeyProbers, proberPart.String()),
	)
	return nil
}

// RewardCapPerPass is the most a node of the given minimum bond can earn for
// one passed round. Over a year of rounds it adds up to the yearly cap; when
// only a share of nodes is probed each round, each pass is worth
// proportionally more, so the yearly total is the same.
func RewardCapPerPass(params types.Params, minBond uint64) uint64 {
	if params.BlocksPerYear == 0 || params.SampleBps == 0 {
		return 0
	}
	c := math.NewIntFromUint64(minBond).
		Mul(math.NewIntFromUint64(params.RewardCapBpsPerYear)).
		Mul(math.NewIntFromUint64(params.RoundBlocks)).
		Quo(math.NewIntFromUint64(params.BlocksPerYear)).
		Quo(math.NewIntFromUint64(params.SampleBps))
	if !c.IsUint64() {
		return 0
	}
	return c.Uint64()
}

// settleReward turns a node's pending share into an amount and adds it to
// the node's balance. The caller must only call it once the pending round's
// tally is complete, so that the round's total weight is final.
//
// The share is the round's budget divided by weight, never above the node's
// cap, and never above what the pool has free. If the round's budget has
// aged out, the share is forfeited and stays in the pool.
func (k Keeper) settleReward(ctx context.Context, rec *types.NodeRecord) error {
	round, weight := rec.PendingRound, rec.PendingWeight
	if round == 0 {
		return nil
	}
	rec.PendingRound, rec.PendingWeight = 0, 0
	if weight == 0 {
		return nil
	}

	budget, err := k.RoundBudget.Get(ctx, round)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	total, err := k.RoundWeight.Get(ctx, round)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	if total == 0 {
		return nil
	}

	share := math.NewIntFromUint64(budget).Mul(math.NewIntFromUint64(weight)).Quo(math.NewIntFromUint64(total))
	// The weight is the node's cap for one pass.
	share = math.MinInt(share, math.NewIntFromUint64(weight))
	_, _, free, _, err := k.PoolState(ctx)
	if err != nil {
		return err
	}
	share = math.MinInt(share, free)
	if !share.IsPositive() {
		return nil
	}

	owed, err := k.rewardOwed(ctx)
	if err != nil {
		return err
	}
	rec.RewardBalance += share.Uint64()
	return k.RewardOwed.Set(ctx, owed+share.Uint64())
}

// earnReward notes a node as an earner of the given round's budget.
func (k Keeper) earnReward(ctx context.Context, rec *types.NodeRecord, node registrytypes.Node, round uint64, params types.Params) error {
	// A node earns once per round, even if its reports were counted in two
	// parts.
	if rec.PendingRound == round {
		return nil
	}
	has, err := k.RoundBudget.Has(ctx, round)
	if err != nil || !has {
		return err
	}
	minBond, err := k.registryKeeper.MinBondFor(ctx, node.Tier)
	if err != nil {
		return err
	}
	aimed, err := k.aimedBps(ctx, params, round, node.Id)
	if err != nil {
		return err
	}
	weight := PassWeight(RewardCapPerPass(params, minBond), params.SampleBps, aimed)
	if weight == 0 {
		return nil
	}

	total, err := k.RoundWeight.Get(ctx, round)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		total = 0
	}
	if err := k.RoundWeight.Set(ctx, round, total+weight); err != nil {
		return err
	}
	rec.PendingRound, rec.PendingWeight = round, weight
	return nil
}

// roundTallied reports whether every report of the round has been counted.
// Reports are stored in round order and deleted as they are counted, so the
// round is done once the first stored report belongs to a later round.
func (k Keeper) roundTallied(ctx context.Context, round uint64) (bool, error) {
	current, _, err := k.CurrentRound(ctx)
	if err != nil {
		return false, err
	}
	// Reports of a round are counted from two rounds later.
	if current < round+2 {
		return false, nil
	}
	iter, err := k.Reports.Iterate(ctx, nil)
	if err != nil {
		return false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return true, nil
	}
	key, err := iter.Key()
	if err != nil {
		return false, err
	}
	return key.K1() > round, nil
}

// dropNodeRecord deletes a node's record. Any reward the node had not
// claimed goes back to the pool's free balance.
func (k Keeper) dropNodeRecord(ctx context.Context, nodeID uint64) error {
	rec, err := k.NodeRecord.Get(ctx, nodeID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	if rec.RewardBalance > 0 {
		owed, err := k.rewardOwed(ctx)
		if err != nil {
			return err
		}
		if rec.RewardBalance > owed {
			owed = rec.RewardBalance
		}
		if err := k.RewardOwed.Set(ctx, owed-rec.RewardBalance); err != nil {
			return err
		}
	}
	return k.NodeRecord.Remove(ctx, nodeID)
}
