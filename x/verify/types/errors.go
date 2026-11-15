package types

// DONTCOVER

import (
	"cosmossdk.io/errors"
)

// x/verify module sentinel errors
var (
	ErrInvalidSigner       = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrInvalidParams       = errors.Register(ModuleName, 1101, "invalid module parameters")
	ErrBondTooLow          = errors.Register(ModuleName, 1102, "bond is below the minimum for a prober")
	ErrAlreadyProber       = errors.Register(ModuleName, 1103, "this address already has a prober")
	ErrProberNotFound      = errors.Register(ModuleName, 1104, "prober not found")
	ErrProberNotActive     = errors.Register(ModuleName, 1105, "prober is not in the active set")
	ErrProberLeaving       = errors.Register(ModuleName, 1106, "prober has already asked to leave")
	ErrProberNotUnbonding  = errors.Register(ModuleName, 1107, "prober is not unbonding")
	ErrUnbondingNotOver    = errors.Register(ModuleName, 1108, "unbonding period has not ended")
	ErrNoRound             = errors.Register(ModuleName, 1109, "no verification round has started yet")
	ErrWrongRound          = errors.Register(ModuleName, 1110, "wrong round")
	ErrInvalidReport       = errors.Register(ModuleName, 1111, "invalid report")
	ErrNodeNotActive       = errors.Register(ModuleName, 1112, "node is not active")
	ErrNodeNotSelected     = errors.Register(ModuleName, 1113, "node is not probed this round")
	ErrNotAssigned         = errors.Register(ModuleName, 1114, "prober is not assigned to this node")
	ErrAlreadyCommitted    = errors.Register(ModuleName, 1115, "a sealed report already exists for this node")
	ErrNoCommit            = errors.Register(ModuleName, 1116, "no sealed report to open")
	ErrAlreadyRevealed     = errors.Register(ModuleName, 1117, "report was already opened")
	ErrRevealMismatch      = errors.Register(ModuleName, 1118, "opened values do not match the sealed report")
	ErrInvalidProber       = errors.Register(ModuleName, 1119, "invalid prober record")
	ErrJailingNotAvailable = errors.Register(ModuleName, 1120, "jailing is not enabled in this version")
)
