package types

import (
	errorsmod "cosmossdk.io/errors"
)

// Default parameter values. These are starting points for test networks and
// must be reviewed before any public launch.
var (
	// DefaultIssuanceDeadlineBlocks is how long issuers have to sign a
	// purchase before the buyer is refunded: about 8 minutes at 5 seconds a block.
	DefaultIssuanceDeadlineBlocks uint64 = 100
	// DefaultMaxBatch is the most tickets in one purchase or redemption.
	DefaultMaxBatch uint64 = 100
	// DefaultFeeBps is the protocol fee in basis points: 500 = 5%.
	DefaultFeeBps uint64 = 500
	// DefaultRetentionBlocks is how long a fulfilled purchase, with its blind
	// signatures, stays readable: about one day at 5 seconds a block.
	DefaultRetentionBlocks uint64 = 17_280
)

// Bounds governance must stay within.
const (
	MaxIssuanceDeadlineBlocks uint64 = 100_000
	// HardMaxBatch bounds the work one transaction can ask of every validator.
	HardMaxBatch uint64 = 1_000
	// MaxFeeBps caps the protocol fee at 50%, so nodes always keep at least half.
	MaxFeeBps          uint64 = 5_000
	MaxRetentionBlocks uint64 = 10_000_000
	// Bridge hand-out bounds.
	MaxBridgeAnswerBlocks uint64 = 100_000
	MaxBridgeTries        uint64 = 20
	MaxBridgeHandoutLimit uint64 = 10_000
	// BasisPoints is the denominator for FeeBps.
	BasisPoints uint64 = 10_000
)

// How the protocol fee on each redeemed ticket is divided, in basis points of
// the fee. The remainder, including any rounding dust, goes to the prober
// fund. These are fixed in code for now; making them governance parameters is
// a later change.
const (
	// FeeShareStakersBps goes to the fee collector, and from there to
	// validators and their delegators: 50%.
	FeeShareStakersBps uint64 = 5_000
	// FeeShareCommunityBps goes to the community pool: 30%.
	FeeShareCommunityBps uint64 = 3_000
	// The prober fund receives what is left: 20%.
)

// NewParams creates a new Params instance.
func NewParams(
	issuanceDeadlineBlocks uint64,
	maxBatch uint64,
	feeBps uint64,
	retentionBlocks uint64,
) Params {
	return Params{
		IssuanceDeadlineBlocks: issuanceDeadlineBlocks,
		MaxBatch:               maxBatch,
		FeeBps:                 feeBps,
		RetentionBlocks:        retentionBlocks,
		BridgeAnswerBlocks:     DefaultBridgeAnswerBlocks,
		BridgeTries:            DefaultBridgeTries,
		BridgeHandoutLimit:     DefaultBridgeHandoutLimit,
		BridgeReportBlocks:     DefaultBridgeReportBlocks,
		BridgeBadSlashBps:      DefaultBridgeBadSlashBps,
		BridgeDeadReports:      DefaultBridgeDeadReports,
		BridgeDeadPct:          DefaultBridgeDeadPct,
	}
}

// Draft bridge hand-out values: a bridge has about 100 blocks to answer,
// three bridges are asked before a request lapses, and one bridge line is
// given to at most 20 people before the bridge must move to be handed out
// again.
const (
	DefaultBridgeAnswerBlocks uint64 = 100
	DefaultBridgeTries        uint64 = 3
	DefaultBridgeHandoutLimit uint64 = 20
	// A bridge is paid about an hour after answering; a proven rubbish
	// answer burns 10% of its bond; a line stops being handed out once at
	// least 3 people, and a quarter of those given it, report it dead.
	DefaultBridgeReportBlocks uint64 = 720
	DefaultBridgeBadSlashBps  uint64 = 1_000
	DefaultBridgeDeadReports  uint64 = 3
	DefaultBridgeDeadPct      uint64 = 25
	MaxBridgeReportBlocks     uint64 = 100_000
)

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return NewParams(
		DefaultIssuanceDeadlineBlocks,
		DefaultMaxBatch,
		DefaultFeeBps,
		DefaultRetentionBlocks,
	)
}

// Validate validates the set of params.
func (p Params) Validate() error {
	if err := validateIssuanceDeadlineBlocks(p.IssuanceDeadlineBlocks); err != nil {
		return err
	}

	if err := validateMaxBatch(p.MaxBatch); err != nil {
		return err
	}

	if err := validateFeeBps(p.FeeBps); err != nil {
		return err
	}

	if err := validateRetentionBlocks(p.RetentionBlocks); err != nil {
		return err
	}

	if p.BridgeAnswerBlocks == 0 || p.BridgeAnswerBlocks > MaxBridgeAnswerBlocks {
		return errorsmod.Wrapf(ErrInvalidParams, "bridge_answer_blocks must be 1-%d, got %d", MaxBridgeAnswerBlocks, p.BridgeAnswerBlocks)
	}
	if p.BridgeTries == 0 || p.BridgeTries > MaxBridgeTries {
		return errorsmod.Wrapf(ErrInvalidParams, "bridge_tries must be 1-%d, got %d", MaxBridgeTries, p.BridgeTries)
	}
	if p.BridgeHandoutLimit == 0 || p.BridgeHandoutLimit > MaxBridgeHandoutLimit {
		return errorsmod.Wrapf(ErrInvalidParams, "bridge_handout_limit must be 1-%d, got %d", MaxBridgeHandoutLimit, p.BridgeHandoutLimit)
	}
	if p.BridgeReportBlocks == 0 || p.BridgeReportBlocks > MaxBridgeReportBlocks {
		return errorsmod.Wrapf(ErrInvalidParams, "bridge_report_blocks must be 1-%d, got %d", MaxBridgeReportBlocks, p.BridgeReportBlocks)
	}
	if p.BridgeBadSlashBps > BasisPoints {
		return errorsmod.Wrapf(ErrInvalidParams, "bridge_bad_slash_bps must be 0-%d", BasisPoints)
	}
	if p.BridgeDeadReports == 0 || p.BridgeDeadPct == 0 || p.BridgeDeadPct > 100 {
		return errorsmod.Wrap(ErrInvalidParams, "bridge_dead_reports must be at least 1 and bridge_dead_pct 1-100")
	}

	return nil
}

// validateIssuanceDeadlineBlocks validates the IssuanceDeadlineBlocks parameter.
func validateIssuanceDeadlineBlocks(v uint64) error {
	if v == 0 || v > MaxIssuanceDeadlineBlocks {
		return errorsmod.Wrapf(ErrInvalidParams, "issuance_deadline_blocks must be 1-%d, got %d", MaxIssuanceDeadlineBlocks, v)
	}
	return nil
}

// validateMaxBatch validates the MaxBatch parameter.
func validateMaxBatch(v uint64) error {
	if v == 0 || v > HardMaxBatch {
		return errorsmod.Wrapf(ErrInvalidParams, "max_batch must be 1-%d, got %d", HardMaxBatch, v)
	}
	return nil
}

// validateFeeBps validates the FeeBps parameter.
func validateFeeBps(v uint64) error {
	if v > MaxFeeBps {
		return errorsmod.Wrapf(ErrInvalidParams, "fee_bps must be 0-%d, got %d", MaxFeeBps, v)
	}
	return nil
}

// validateRetentionBlocks validates the RetentionBlocks parameter.
func validateRetentionBlocks(v uint64) error {
	if v == 0 || v > MaxRetentionBlocks {
		return errorsmod.Wrapf(ErrInvalidParams, "retention_blocks must be 1-%d, got %d", MaxRetentionBlocks, v)
	}
	return nil
}

// BridgePoolID is the node id a ticket names when it pays for a bridge
// line rather than for service at a node. No node can have this id.
const BridgePoolID uint64 = 1<<64 - 1

// Bridge request states.
const (
	BridgeOpen     uint32 = 1
	BridgeAnswered uint32 = 2
	BridgeLapsed   uint32 = 3
)
