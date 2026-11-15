package types

// DONTCOVER

import (
	"cosmossdk.io/errors"
)

// x/tickets module sentinel errors
var (
	ErrInvalidSigner     = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrInvalidParams     = errors.Register(ModuleName, 1101, "invalid module parameters")
	ErrInvalidIssuerKey  = errors.Register(ModuleName, 1102, "invalid issuer key")
	ErrIssuerKeyNotFound = errors.Register(ModuleName, 1103, "issuer key not found")
	ErrKeyNotSigning     = errors.Register(ModuleName, 1104, "issuer key is no longer signing new tickets")
	ErrKeyExpired        = errors.Register(ModuleName, 1105, "issuer key has passed its redemption deadline")
	ErrInvalidBatch      = errors.Register(ModuleName, 1106, "invalid batch size")
	ErrInvalidBlinded    = errors.Register(ModuleName, 1107, "invalid blinded ticket")
	ErrDuplicateBlinded  = errors.Register(ModuleName, 1108, "blinded ticket appears twice in one purchase")
	ErrPurchaseNotFound  = errors.Register(ModuleName, 1109, "purchase not found")
	ErrAlreadyFulfilled  = errors.Register(ModuleName, 1110, "purchase is already fulfilled")
	ErrBadBlindSignature = errors.Register(ModuleName, 1111, "blind signature does not verify")
	ErrInvalidTicket     = errors.Register(ModuleName, 1112, "invalid ticket")
	ErrTicketSpent       = errors.Register(ModuleName, 1113, "ticket has already been redeemed")
	ErrNotNodeOperator   = errors.Register(ModuleName, 1114, "signer is not the operator of this node")
	ErrNodeNotActive     = errors.Register(ModuleName, 1115, "node is not active")
	ErrOverRedemption    = errors.Register(ModuleName, 1116, "more tickets redeemed than were issued under this key")
	ErrInvalidGenesis    = errors.Register(ModuleName, 1117, "invalid genesis state")
)
