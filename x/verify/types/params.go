package types

// DefaultRoundBlocks represents the RoundBlocks default value.
// TODO: Determine the default value.
var DefaultRoundBlocks uint64 = 0

// DefaultProbersPerNode represents the ProbersPerNode default value.
// TODO: Determine the default value.
var DefaultProbersPerNode uint64 = 0

// DefaultSampleBps represents the SampleBps default value.
// TODO: Determine the default value.
var DefaultSampleBps uint64 = 0

// DefaultMinProberBond represents the MinProberBond default value.
// TODO: Determine the default value.
var DefaultMinProberBond uint64 = 0

// DefaultUnbondingSeconds represents the UnbondingSeconds default value.
// TODO: Determine the default value.
var DefaultUnbondingSeconds uint64 = 0

// DefaultFailsBeforeJail represents the FailsBeforeJail default value.
// TODO: Determine the default value.
var DefaultFailsBeforeJail uint64 = 0

// DefaultTamperSlashBps represents the TamperSlashBps default value.
// TODO: Determine the default value.
var DefaultTamperSlashBps uint64 = 0

// DefaultReportOnly represents the ReportOnly default value.
// TODO: Determine the default value.
var DefaultReportOnly bool = true

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
	if err := validateRoundBlocks(p.RoundBlocks); err != nil {
		return err
	}

	if err := validateProbersPerNode(p.ProbersPerNode); err != nil {
		return err
	}

	if err := validateSampleBps(p.SampleBps); err != nil {
		return err
	}

	if err := validateMinProberBond(p.MinProberBond); err != nil {
		return err
	}

	if err := validateUnbondingSeconds(p.UnbondingSeconds); err != nil {
		return err
	}

	if err := validateFailsBeforeJail(p.FailsBeforeJail); err != nil {
		return err
	}

	if err := validateTamperSlashBps(p.TamperSlashBps); err != nil {
		return err
	}

	if err := validateReportOnly(p.ReportOnly); err != nil {
		return err
	}

	return nil
}

// validateRoundBlocks validates the RoundBlocks parameter.
func validateRoundBlocks(v uint64) error {
	// TODO implement validation
	return nil
}

// validateProbersPerNode validates the ProbersPerNode parameter.
func validateProbersPerNode(v uint64) error {
	// TODO implement validation
	return nil
}

// validateSampleBps validates the SampleBps parameter.
func validateSampleBps(v uint64) error {
	// TODO implement validation
	return nil
}

// validateMinProberBond validates the MinProberBond parameter.
func validateMinProberBond(v uint64) error {
	// TODO implement validation
	return nil
}

// validateUnbondingSeconds validates the UnbondingSeconds parameter.
func validateUnbondingSeconds(v uint64) error {
	// TODO implement validation
	return nil
}

// validateFailsBeforeJail validates the FailsBeforeJail parameter.
func validateFailsBeforeJail(v uint64) error {
	// TODO implement validation
	return nil
}

// validateTamperSlashBps validates the TamperSlashBps parameter.
func validateTamperSlashBps(v uint64) error {
	// TODO implement validation
	return nil
}

// validateReportOnly validates the ReportOnly parameter.
func validateReportOnly(v bool) error {
	// TODO implement validation
	return nil
}
