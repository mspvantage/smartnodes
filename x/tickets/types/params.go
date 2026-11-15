package types

// DefaultIssuanceDeadlineBlocks represents the IssuanceDeadlineBlocks default value.
// TODO: Determine the default value.
var DefaultIssuanceDeadlineBlocks uint64 = 0

// DefaultMaxBatch represents the MaxBatch default value.
// TODO: Determine the default value.
var DefaultMaxBatch uint64 = 0

// DefaultFeeBps represents the FeeBps default value.
// TODO: Determine the default value.
var DefaultFeeBps uint64 = 0

// DefaultRetentionBlocks represents the RetentionBlocks default value.
// TODO: Determine the default value.
var DefaultRetentionBlocks uint64 = 0

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
	}
}

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

	return nil
}

// validateIssuanceDeadlineBlocks validates the IssuanceDeadlineBlocks parameter.
func validateIssuanceDeadlineBlocks(v uint64) error {
	// TODO implement validation
	return nil
}

// validateMaxBatch validates the MaxBatch parameter.
func validateMaxBatch(v uint64) error {
	// TODO implement validation
	return nil
}

// validateFeeBps validates the FeeBps parameter.
func validateFeeBps(v uint64) error {
	// TODO implement validation
	return nil
}

// validateRetentionBlocks validates the RetentionBlocks parameter.
func validateRetentionBlocks(v uint64) error {
	// TODO implement validation
	return nil
}
