package types

// DONTCOVER

import (
	"cosmossdk.io/errors"
)

// x/rewards module sentinel errors
var (
	ErrInvalidSigner = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrInvalidParams = errors.Register(ModuleName, 1101, "invalid module parameters")
	// ErrNonVotingListGrew is returned when a parameter change tries to add
	// an address to the non-voting list. The list may only shrink.
	ErrNonVotingListGrew = errors.Register(ModuleName, 1103, "the non-voting list can only shrink")
	// ErrCommissionTooHigh is returned when a new validator sets a maximum
	// commission above the ceiling.
	ErrCommissionTooHigh = errors.Register(ModuleName, 1102, "maximum commission is above the ceiling")
)
