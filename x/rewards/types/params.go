package types

// DefaultEqualShareBps represents the EqualShareBps default value.
// TODO: Determine the default value.
var DefaultEqualShareBps uint64 = 0

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

// validateEqualShareBps validates the EqualShareBps parameter.
func validateEqualShareBps(v uint64) error {
	// TODO implement validation
	return nil
}
