package types

// DONTCOVER

import (
	"cosmossdk.io/errors"
)

// x/rewards module sentinel errors
var (
	ErrInvalidSigner = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrInvalidParams = errors.Register(ModuleName, 1101, "invalid module parameters")
	// ErrCommissionTooHigh is returned when a new validator sets a maximum
	// commission above the ceiling.
	ErrCommissionTooHigh = errors.Register(ModuleName, 1102, "maximum commission is above the ceiling")
)
