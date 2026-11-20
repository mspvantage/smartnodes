package types

// DONTCOVER

import (
	"cosmossdk.io/errors"
)

// x/registry module sentinel errors
var (
	ErrInvalidSigner      = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrInvalidTier        = errors.Register(ModuleName, 1101, "invalid node tier")
	ErrInvalidEndpoint    = errors.Register(ModuleName, 1102, "invalid node endpoint")
	ErrInvalidPubkey      = errors.Register(ModuleName, 1103, "invalid node public key")
	ErrInvalidBond        = errors.Register(ModuleName, 1104, "invalid bond")
	ErrBondTooLow         = errors.Register(ModuleName, 1105, "bond is below the minimum for this tier")
	ErrPubkeyTaken        = errors.Register(ModuleName, 1106, "public key is already registered")
	ErrNodeNotFound       = errors.Register(ModuleName, 1107, "node not found")
	ErrNotOperator        = errors.Register(ModuleName, 1108, "signer is not the operator of this node")
	ErrNodeNotActive      = errors.Register(ModuleName, 1109, "node is not active")
	ErrNodeNotUnbonding   = errors.Register(ModuleName, 1110, "node is not unbonding")
	ErrUnbondingNotOver   = errors.Register(ModuleName, 1111, "unbonding period has not ended")
	ErrInvalidNode        = errors.Register(ModuleName, 1112, "invalid node record")
	ErrInvalidParams      = errors.Register(ModuleName, 1113, "invalid module parameters")
	ErrNothingToUpdate    = errors.Register(ModuleName, 1114, "no change requested")
	ErrInvalidSlashAmount = errors.Register(ModuleName, 1115, "invalid slash amount")
	ErrSponsorshipOff     = errors.Register(ModuleName, 1116, "sponsored bonds are switched off")
	ErrNotSponsor         = errors.Register(ModuleName, 1117, "signer is not the sponsor account")
	ErrSponsorLimit       = errors.Register(ModuleName, 1118, "the limit of sponsored nodes has been reached")
	ErrInvalidAmount      = errors.Register(ModuleName, 1119, "invalid amount")
	ErrInvalidServiceKey  = errors.Register(ModuleName, 1120, "invalid service key")
	ErrInvalidReality     = errors.Register(ModuleName, 1121, "invalid disguise")
	ErrIsBridge           = errors.Register(ModuleName, 1122, "the node is a bridge")
	ErrInvalidCommitment  = errors.Register(ModuleName, 1123, "invalid bridge fingerprint")
)
