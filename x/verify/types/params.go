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
	// DefaultReportOnly records results but applies no penalties.
	DefaultReportOnly bool = true
)

const (
	// MinRoundBlocks is the shortest round allowed. A prober needs time to
	// probe and post a sealed report inside one round.
	MinRoundBlocks = 5
	// MaxRoundBlocks is the longest round allowed: about a week.
	MaxRoundBlocks = 120_960
	// MaxUnbondingSeconds is the longest unbonding period allowed: one year.
	MaxUnbondingSeconds = 365 * 24 * 60 * 60
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
	// This version can record results but cannot yet apply penalties, so it
	// refuses to be configured as if it could.
	if !p.ReportOnly {
		return errorsmod.Wrap(ErrInvalidParams, "report_only must be true: penalties are not built in this version")
	}
	return nil
}
