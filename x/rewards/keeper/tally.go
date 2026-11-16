package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govkeeper "github.com/cosmos/cosmos-sdk/x/gov/keeper"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/mspvantage/smartnodes/x/rewards/types"
)

// Governance vote counting with non-voting stake
//
// The standard count works like this: every validator votes with all the
// stake delegated to it, except that a delegator who votes directly takes
// their own stake out of the validator's pile and votes with it themselves.
//
// This count is the same, with one addition. Stake delegated by an account
// on the non-voting list is taken out of every pile before counting starts:
//
//   - a vote sent by a listed account is ignored;
//   - the validators it is delegated to do not vote with it;
//   - it is left out of the total that the quorum is measured against.
//
// The stake still earns rewards and still counts for block production. Only
// its say in governance is removed.

// nonVotingSet returns the non-voting list as raw address bytes.
func (k Keeper) nonVotingSet(ctx context.Context) (map[string]bool, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return nil, err
		}
		params = types.DefaultParams()
	}
	set := make(map[string]bool, len(params.NonVotingAddresses))
	for _, text := range params.NonVotingAddresses {
		addr, err := k.addressCodec.StringToBytes(text)
		if err != nil {
			// Genesis import refuses a list with an unreadable address, so
			// this cannot happen on a running chain.
			return nil, fmt.Errorf("non-voting address %q: %w", text, err)
		}
		set[string(addr)] = true
	}
	return set, nil
}

// IsNonVoting reports whether an account is on the non-voting list.
func (k Keeper) IsNonVoting(ctx context.Context, addr sdk.AccAddress) (bool, error) {
	set, err := k.nonVotingSet(ctx)
	if err != nil {
		return false, err
	}
	return set[string(addr)], nil
}

// CountVotes tallies the given votes. It returns the power that voted, the
// total power the quorum is measured against, and the power behind each
// option.
func (k Keeper) CountVotes(ctx context.Context, votes []v1.Vote) (totalVoterPower math.LegacyDec, totalValPower math.Int, results map[v1.VoteOption]math.LegacyDec, err error) {
	sk := k.stakingKeeper

	results = map[v1.VoteOption]math.LegacyDec{
		v1.OptionYes:        math.LegacyZeroDec(),
		v1.OptionAbstain:    math.LegacyZeroDec(),
		v1.OptionNo:         math.LegacyZeroDec(),
		v1.OptionNoWithVeto: math.LegacyZeroDec(),
	}
	fail := func(err error) (math.LegacyDec, math.Int, map[v1.VoteOption]math.LegacyDec, error) {
		return math.LegacyZeroDec(), math.ZeroInt(), nil, err
	}

	// Every validator in the active set, with its bonded tokens and shares.
	validators := make(map[string]v1.ValidatorGovInfo)
	totalValPower = math.ZeroInt()
	if err := sk.IterateBondedValidatorsByPower(ctx, func(_ int64, validator stakingtypes.ValidatorI) (stop bool) {
		valBz, err := sk.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
		if err != nil {
			return false
		}
		shares := validator.GetDelegatorShares()
		if shares.IsNil() || !shares.IsPositive() {
			// A validator with no shares has nothing to vote with.
			return false
		}
		power := validator.GetValidatorPower()
		validators[validator.GetOperator()] = v1.NewValidatorGovInfo(
			valBz,
			power,
			shares,
			math.LegacyZeroDec(),
			v1.WeightedVoteOptions{},
		)
		totalValPower = totalValPower.Add(power)
		return false
	}); err != nil {
		return fail(err)
	}

	// Take non-voting stake out of every validator's pile and out of the
	// quorum total, before any vote is looked at.
	nonVoting, err := k.nonVotingSet(ctx)
	if err != nil {
		return fail(err)
	}
	excluded := math.LegacyZeroDec()
	for addr := range nonVoting {
		if err := sk.IterateDelegations(ctx, sdk.AccAddress(addr), func(_ int64, delegation stakingtypes.DelegationI) (stop bool) {
			val, ok := validators[delegation.GetValidatorAddr()]
			if !ok {
				return false
			}
			val.DelegatorDeductions = val.DelegatorDeductions.Add(delegation.GetShares())
			validators[delegation.GetValidatorAddr()] = val
			excluded = excluded.Add(delegation.GetShares().MulInt(val.BondedTokens).Quo(val.DelegatorShares))
			return false
		}); err != nil {
			return fail(err)
		}
	}
	// Round the excluded power down, so the quorum total is never understated.
	totalValPower = totalValPower.Sub(excluded.TruncateInt())
	if totalValPower.IsNegative() {
		totalValPower = math.ZeroInt()
	}

	totalVoterPower = math.LegacyZeroDec()

	for _, vote := range votes {
		voter, err := k.addressCodec.StringToBytes(vote.Voter)
		if err != nil {
			return fail(err)
		}
		// A listed account has no say, directly or as a validator operator.
		if nonVoting[string(voter)] {
			continue
		}

		// A validator operator's vote is noted now and counted at the end,
		// once its delegators' own votes have been taken out of its pile.
		valAddrStr, err := sk.ValidatorAddressCodec().BytesToString(voter)
		if err != nil {
			return fail(err)
		}
		if val, ok := validators[valAddrStr]; ok {
			val.Vote = vote.Options
			validators[valAddrStr] = val
		}

		// The voter votes with their own delegations, which leave the piles
		// of the validators they are delegated to.
		if err := sk.IterateDelegations(ctx, voter, func(_ int64, delegation stakingtypes.DelegationI) (stop bool) {
			val, ok := validators[delegation.GetValidatorAddr()]
			if !ok {
				return false
			}
			val.DelegatorDeductions = val.DelegatorDeductions.Add(delegation.GetShares())
			validators[delegation.GetValidatorAddr()] = val

			votingPower := delegation.GetShares().MulInt(val.BondedTokens).Quo(val.DelegatorShares)
			for _, option := range vote.Options {
				weight, _ := math.LegacyNewDecFromStr(option.Weight)
				results[option.Option] = results[option.Option].Add(votingPower.Mul(weight))
			}
			totalVoterPower = totalVoterPower.Add(votingPower)
			return false
		}); err != nil {
			return fail(err)
		}
	}

	// Validators that voted, with whatever is left in their pile.
	for _, val := range validators {
		if len(val.Vote) == 0 {
			continue
		}
		sharesLeft := val.DelegatorShares.Sub(val.DelegatorDeductions)
		if !sharesLeft.IsPositive() {
			continue
		}
		votingPower := sharesLeft.MulInt(val.BondedTokens).Quo(val.DelegatorShares)
		for _, option := range val.Vote {
			weight, _ := math.LegacyNewDecFromStr(option.Weight)
			results[option.Option] = results[option.Option].Add(votingPower.Mul(weight))
		}
		totalVoterPower = totalVoterPower.Add(votingPower)
	}

	return totalVoterPower, totalValPower, results, nil
}

// GovTally returns the vote-counting function the governance module uses in
// place of its own. It reads the proposal's votes, deletes them as the
// standard count does, and counts them with CountVotes.
func (k Keeper) GovTally() govkeeper.CalculateVoteResultsAndVotingPowerFn {
	return func(ctx context.Context, gk govkeeper.Keeper, proposal v1.Proposal) (math.LegacyDec, math.Int, map[v1.VoteOption]math.LegacyDec, error) {
		var votes []v1.Vote
		var keys []collections.Pair[uint64, sdk.AccAddress]
		rng := collections.NewPrefixedPairRange[uint64, sdk.AccAddress](proposal.Id)
		err := gk.Votes.Walk(ctx, rng, func(key collections.Pair[uint64, sdk.AccAddress], vote v1.Vote) (bool, error) {
			votes = append(votes, vote)
			keys = append(keys, key)
			return false, nil
		})
		if err != nil {
			return math.LegacyZeroDec(), math.ZeroInt(), nil, fmt.Errorf("error while reading votes: %w", err)
		}

		totalVoterPower, totalValPower, results, err := k.CountVotes(ctx, votes)
		if err != nil {
			return math.LegacyZeroDec(), math.ZeroInt(), nil, err
		}

		for _, key := range keys {
			if err := gk.Votes.Remove(ctx, key); err != nil {
				return math.LegacyZeroDec(), math.ZeroInt(), nil, fmt.Errorf("error while removing vote (%d/%s): %w", key.K1(), key.K2(), err)
			}
		}
		return totalVoterPower, totalValPower, results, nil
	}
}
