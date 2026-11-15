package types

// DefaultMinBondEntry represents the MinBondEntry default value.
// TODO: Determine the default value.
var DefaultMinBondEntry uint64 = 0

// DefaultMinBondExit represents the MinBondExit default value.
// TODO: Determine the default value.
var DefaultMinBondExit uint64 = 0

// DefaultUnbondingSeconds represents the UnbondingSeconds default value.
// TODO: Determine the default value.
var DefaultUnbondingSeconds uint64 = 0

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

// validateMinBondEntry validates the MinBondEntry parameter.
func validateMinBondEntry(v uint64) error {
	// TODO implement validation
	return nil
}

// validateMinBondExit validates the MinBondExit parameter.
func validateMinBondExit(v uint64) error {
	// TODO implement validation
	return nil
}

// validateUnbondingSeconds validates the UnbondingSeconds parameter.
func validateUnbondingSeconds(v uint64) error {
	// TODO implement validation
	return nil
}
