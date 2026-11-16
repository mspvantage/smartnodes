package types

import (
	errorsmod "cosmossdk.io/errors"
)

// BasisPoints is the denominator for EqualShareBps.
const BasisPoints uint64 = 10_000

// DefaultEqualShareBps is the share of validator income paid equally to every
// active validator, in basis points: 5,000 = half. The rest is paid by stake
// in the standard way.
var DefaultEqualShareBps uint64 = 5_000

// DefaultMaxCommissionBps is the commission ceiling: 2,000 = 20%. A new
// validator's maximum commission may not be above it. Staking already stops a
// validator raising its commission above its own maximum, so checking the
// maximum once, at creation, caps the commission for good.
var DefaultMaxCommissionBps uint64 = 2_000

// DefaultEqualShareMinSelfStake is the least an operator must stake with
// their own validator for it to receive the equal share: 5,000 SNODE. It is a
// placeholder to be set with the token-economics review.
//
// The equal share pays every validator the same, so without a minimum one
// operator could run several small validators to collect several shares. The
// chain cannot tell that two validators belong to one person, but it can make
// each one cost real stake of the operator's own.
var DefaultEqualShareMinSelfStake uint64 = 5_000_000_000

// NewParams creates a new Params instance.
func NewParams(
	equalShareBps uint64,
) Params {
	return Params{
		EqualShareBps:          equalShareBps,
		MaxCommissionBps:       DefaultMaxCommissionBps,
		EqualShareMinSelfStake: DefaultEqualShareMinSelfStake,
	}
}

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return NewParams(
		DefaultEqualShareBps,
	)
}

// Validate validates the set of params.
func (p Params) Validate() error {
	if err := validateEqualShareBps(p.EqualShareBps); err != nil {
		return err
	}
	// 10,000 means no ceiling. Zero is refused: it would stop any validator
	// from ever being created.
	if p.MaxCommissionBps < 1 || p.MaxCommissionBps > BasisPoints {
		return errorsmod.Wrapf(ErrInvalidParams, "max_commission_bps must be 1-%d, got %d", BasisPoints, p.MaxCommissionBps)
	}

	return nil
}

// validateEqualShareBps validates the EqualShareBps parameter. Zero turns the
// equal share off; 10,000 pays everything equally.
func validateEqualShareBps(v uint64) error {
	if v > BasisPoints {
		return errorsmod.Wrapf(ErrInvalidParams, "equal_share_bps must be 0-%d, got %d", BasisPoints, v)
	}
	return nil
}
