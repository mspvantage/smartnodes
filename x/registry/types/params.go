package types

import (
	errorsmod "cosmossdk.io/errors"
)

// Default parameter values. These are starting points for test networks and
// must be reviewed before any public launch. Bond amounts are in the base
// unit of the staking token (usnode: 1 SNODE = 1,000,000 usnode).
var (
	// DefaultMinBondEntry is 1,000 SNODE.
	DefaultMinBondEntry uint64 = 1_000_000_000
	// DefaultMinBondExit is 5,000 SNODE. Exit nodes carry more responsibility,
	// so they post a larger bond.
	DefaultMinBondExit uint64 = 5_000_000_000
	// DefaultUnbondingSeconds is 21 days, matching the staking unbonding time.
	DefaultUnbondingSeconds uint64 = 21 * 24 * 60 * 60
)

const (
	// MinUnbondingSeconds is the shortest unbonding period governance may set.
	// A bond that can leave instantly cannot be slashed for late-found faults.
	MinUnbondingSeconds uint64 = 60 * 60
	// MaxUnbondingSeconds is the longest unbonding period governance may set.
	// The cap stops a parameter change from locking bonds indefinitely.
	MaxUnbondingSeconds uint64 = 365 * 24 * 60 * 60
)

// NewParams creates a new Params instance.
func NewParams(
	minBondEntry uint64,
	minBondExit uint64,
	unbondingSeconds uint64,
) Params {
	return Params{
		MinBondEntry:     minBondEntry,
		MinBondExit:      minBondExit,
		UnbondingSeconds: unbondingSeconds,
	}
}

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return NewParams(
		DefaultMinBondEntry,
		DefaultMinBondExit,
		DefaultUnbondingSeconds,
	)
}

// Validate validates the set of params.
func (p Params) Validate() error {
	if err := validateMinBondEntry(p.MinBondEntry); err != nil {
		return err
	}

	if err := validateMinBondExit(p.MinBondExit); err != nil {
		return err
	}

	if err := validateUnbondingSeconds(p.UnbondingSeconds); err != nil {
		return err
	}

	return nil
}

// MinBond returns the minimum bond for a tier.
func (p Params) MinBond(tier uint64) (uint64, error) {
	switch tier {
	case TierEntry:
		return p.MinBondEntry, nil
	case TierExit:
		return p.MinBondExit, nil
	default:
		return 0, ValidateTier(tier)
	}
}

// validateMinBondEntry validates the MinBondEntry parameter.
func validateMinBondEntry(v uint64) error {
	if v == 0 {
		return errorsmod.Wrap(ErrInvalidParams, "min_bond_entry must be greater than zero")
	}
	return nil
}

// validateMinBondExit validates the MinBondExit parameter.
func validateMinBondExit(v uint64) error {
	if v == 0 {
		return errorsmod.Wrap(ErrInvalidParams, "min_bond_exit must be greater than zero")
	}
	return nil
}

// validateUnbondingSeconds validates the UnbondingSeconds parameter.
func validateUnbondingSeconds(v uint64) error {
	if v < MinUnbondingSeconds || v > MaxUnbondingSeconds {
		return errorsmod.Wrapf(ErrInvalidParams, "unbonding_seconds must be %d-%d, got %d", MinUnbondingSeconds, MaxUnbondingSeconds, v)
	}
	return nil
}
