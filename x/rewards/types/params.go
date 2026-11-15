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

// NewParams creates a new Params instance.
func NewParams(
	equalShareBps uint64,
) Params {
	return Params{
		EqualShareBps: equalShareBps,
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
