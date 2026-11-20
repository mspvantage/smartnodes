package types

import (
	errorsmod "cosmossdk.io/errors"
)

// Default parameter values. Every number here is a placeholder to be tuned
// on a test network and reviewed before any network carries real value.
var (
	// DefaultRoundBlocks is the length of a verification round: about one
	// hour at five-second blocks.
	DefaultRoundBlocks uint64 = 720
	// DefaultProbersPerNode is how many probers are assigned to each node
	// that is probed in a round.
	DefaultProbersPerNode uint64 = 3
	// DefaultSampleBps is the share of nodes probed each round, in basis
	// points. 10,000 probes every node every round; lower it as the network
	// grows.
	DefaultSampleBps uint64 = 10_000
	// DefaultMinProberBond is the smallest bond a prober may post, in the
	// base unit of the staking token: 1,000 SNODE.
	DefaultMinProberBond uint64 = 1_000_000_000
	// DefaultUnbondingSeconds is how long a prober's bond stays locked after
	// it leaves: 21 days.
	DefaultUnbondingSeconds uint64 = 21 * 24 * 60 * 60
	// DefaultFailsBeforeJail is how many failed rounds in a row lead to
	// jailing.
	DefaultFailsBeforeJail uint64 = 6
	// DefaultTamperSlashBps is the share of a node's bond burned for proven
	// tampering, in basis points.
	DefaultTamperSlashBps uint64 = 1_000
	// DefaultReportOnly records results but applies no penalties. Networks
	// start this way so thresholds can be tuned before anyone loses money.
	DefaultReportOnly bool = true
	// DefaultUnjailWaitSeconds is how long a jailed node waits before its
	// operator may unjail it: ten minutes.
	DefaultUnjailWaitSeconds uint64 = 600
	// DefaultReportReward is paid per accepted report, in the base unit of
	// the staking token: 0.01 SNODE.
	DefaultReportReward uint64 = 10_000
	// DefaultPoolReleaseBpsPerYear releases 20% of what is left in the node
	// rewards pool each year.
	DefaultPoolReleaseBpsPerYear uint64 = 2_000
	// DefaultNodeShareBps gives 80% of each release to verified nodes and
	// the rest to the prober fund.
	DefaultNodeShareBps uint64 = 8_000
	// DefaultRewardCapBpsPerYear caps a node's reward at 30% of its tier's
	// minimum bond per year.
	DefaultRewardCapBpsPerYear uint64 = 3_000
	// DefaultBlocksPerYear assumes five-second blocks.
	DefaultBlocksPerYear uint64 = 6_311_520
	// DefaultRewardRetentionRounds keeps round budgets for 30 days of
	// one-hour rounds.
	DefaultRewardRetentionRounds uint64 = 720
	// DefaultMinEntryMbps and DefaultMinExitMbps are the slowest speeds a
	// probe accepts, in megabits per second. Draft values, to be tuned on
	// the test network.
	DefaultMinEntryMbps uint64 = 5
	DefaultMinExitMbps  uint64 = 5
)

const (
	// MinRoundBlocks is the shortest round allowed. A prober needs time to
	// probe and post a sealed report inside one round.
	MinRoundBlocks = 5
	// MaxRoundBlocks is the longest round allowed: about a week.
	MaxRoundBlocks = 120_960
	// MaxUnbondingSeconds is the longest unbonding period allowed: one year.
	MaxUnbondingSeconds = 365 * 24 * 60 * 60
	// MinRewardRetentionRounds is the shortest time a round budget may be
	// kept. A round is tallied two rounds after it ends, so anything shorter
	// would delete budgets before they could be used.
	MinRewardRetentionRounds = 4
	// MaxRewardRetentionRounds bounds how many round budgets are stored.
	MaxRewardRetentionRounds = 100_000
	// MaxRewardCapBpsPerYear bounds the yearly reward cap at ten times the
	// minimum bond.
	MaxRewardCapBpsPerYear = 100_000
	// MaxMinMbps bounds the minimum speed settings: 100 Gbit/s.
	MaxMinMbps = 100_000
)

// NewParams creates a new Params instance.
func NewParams(
	roundBlocks uint64,
	probersPerNode uint64,
	sampleBps uint64,
	minProberBond uint64,
	unbondingSeconds uint64,
	failsBeforeJail uint64,
	tamperSlashBps uint64,
	reportOnly bool,
	unjailWaitSeconds uint64,
	reportReward uint64,
	poolReleaseBpsPerYear uint64,
	nodeShareBps uint64,
	rewardCapBpsPerYear uint64,
	blocksPerYear uint64,
	rewardRetentionRounds uint64,
	minEntryMbps uint64,
	minExitMbps uint64,
) Params {
	return Params{
		RoundBlocks:      roundBlocks,
		ProbersPerNode:   probersPerNode,
		SampleBps:        sampleBps,
		MinProberBond:    minProberBond,
		UnbondingSeconds: unbondingSeconds,
		FailsBeforeJail:  failsBeforeJail,
		TamperSlashBps:   tamperSlashBps,
		ReportOnly:       reportOnly,

		UnjailWaitSeconds: unjailWaitSeconds,
		ReportReward:      reportReward,

		PoolReleaseBpsPerYear: poolReleaseBpsPerYear,
		NodeShareBps:          nodeShareBps,
		RewardCapBpsPerYear:   rewardCapBpsPerYear,
		BlocksPerYear:         blocksPerYear,
		RewardRetentionRounds: rewardRetentionRounds,
		MinEntryMbps:          minEntryMbps,
		MinExitMbps:           minExitMbps,
	}
}

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return NewParams(
		DefaultRoundBlocks,
		DefaultProbersPerNode,
		DefaultSampleBps,
		DefaultMinProberBond,
		DefaultUnbondingSeconds,
		DefaultFailsBeforeJail,
		DefaultTamperSlashBps,
		DefaultReportOnly,
		DefaultUnjailWaitSeconds,
		DefaultReportReward,
		DefaultPoolReleaseBpsPerYear,
		DefaultNodeShareBps,
		DefaultRewardCapBpsPerYear,
		DefaultBlocksPerYear,
		DefaultRewardRetentionRounds,
		DefaultMinEntryMbps,
		DefaultMinExitMbps,
	)
}

// Validate validates the set of params.
func (p Params) Validate() error {
	if p.RoundBlocks < MinRoundBlocks || p.RoundBlocks > MaxRoundBlocks {
		return errorsmod.Wrapf(ErrInvalidParams, "round_blocks %d, want %d-%d", p.RoundBlocks, MinRoundBlocks, MaxRoundBlocks)
	}
	if p.ProbersPerNode < 1 || p.ProbersPerNode > MaxProbersPerNode {
		return errorsmod.Wrapf(ErrInvalidParams, "probers_per_node %d, want 1-%d", p.ProbersPerNode, MaxProbersPerNode)
	}
	// An even number of probers can tie. Odd numbers always give a majority
	// to one side when every prober reports.
	if p.ProbersPerNode%2 == 0 {
		return errorsmod.Wrapf(ErrInvalidParams, "probers_per_node %d must be odd", p.ProbersPerNode)
	}
	if p.SampleBps < 1 || p.SampleBps > BpsDenominator {
		return errorsmod.Wrapf(ErrInvalidParams, "sample_bps %d, want 1-%d", p.SampleBps, BpsDenominator)
	}
	if p.MinProberBond == 0 {
		return errorsmod.Wrap(ErrInvalidParams, "min_prober_bond must be greater than zero")
	}
	if p.UnbondingSeconds > MaxUnbondingSeconds {
		return errorsmod.Wrapf(ErrInvalidParams, "unbonding_seconds %d, want at most %d", p.UnbondingSeconds, MaxUnbondingSeconds)
	}
	if p.FailsBeforeJail == 0 {
		return errorsmod.Wrap(ErrInvalidParams, "fails_before_jail must be greater than zero")
	}
	if p.TamperSlashBps > BpsDenominator {
		return errorsmod.Wrapf(ErrInvalidParams, "tamper_slash_bps %d, want at most %d", p.TamperSlashBps, BpsDenominator)
	}
	if p.UnjailWaitSeconds > MaxUnbondingSeconds {
		return errorsmod.Wrapf(ErrInvalidParams, "unjail_wait_seconds %d, want at most %d", p.UnjailWaitSeconds, MaxUnbondingSeconds)
	}
	// report_only and report_reward accept any value: false switches jailing
	// and slashing on, and a reward of zero switches prober payments off.
	if p.PoolReleaseBpsPerYear > BpsDenominator {
		return errorsmod.Wrapf(ErrInvalidParams, "pool_release_bps_per_year %d, want at most %d", p.PoolReleaseBpsPerYear, BpsDenominator)
	}
	if p.NodeShareBps > BpsDenominator {
		return errorsmod.Wrapf(ErrInvalidParams, "node_share_bps %d, want at most %d", p.NodeShareBps, BpsDenominator)
	}
	if p.MinEntryMbps > MaxMinMbps || p.MinExitMbps > MaxMinMbps {
		return errorsmod.Wrapf(ErrInvalidParams, "minimum speeds %d and %d Mbit/s, want at most %d", p.MinEntryMbps, p.MinExitMbps, MaxMinMbps)
	}
	if p.RewardCapBpsPerYear > MaxRewardCapBpsPerYear {
		return errorsmod.Wrapf(ErrInvalidParams, "reward_cap_bps_per_year %d, want at most %d", p.RewardCapBpsPerYear, MaxRewardCapBpsPerYear)
	}
	if p.BlocksPerYear == 0 {
		return errorsmod.Wrap(ErrInvalidParams, "blocks_per_year must be greater than zero")
	}
	if p.RewardRetentionRounds < MinRewardRetentionRounds || p.RewardRetentionRounds > MaxRewardRetentionRounds {
		return errorsmod.Wrapf(ErrInvalidParams, "reward_retention_rounds %d, want %d-%d", p.RewardRetentionRounds, MinRewardRetentionRounds, MaxRewardRetentionRounds)
	}
	return nil
}
