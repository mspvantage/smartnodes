package types

import (
	"encoding/base64"
	"net"
	"strconv"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
)

// Node tiers. The tier decides the minimum bond and, in clients, the role a
// node may play in a path.
const (
	// TierEntry nodes accept user connections and forward to an exit node.
	TierEntry uint64 = 1
	// TierExit nodes deliver traffic to the public internet.
	TierExit uint64 = 2
)

// Node statuses.
const (
	// StatusActive nodes are registered, bonded and eligible to serve.
	StatusActive uint64 = 1
	// StatusUnbonding nodes have deregistered and are waiting out the
	// unbonding period. Their bond stays locked and slashable.
	StatusUnbonding uint64 = 2
)

const (
	// MaxEndpointLength is the longest endpoint accepted, in bytes.
	MaxEndpointLength = 255
	// PubkeyLength is the size of a decoded node public key (Curve25519).
	PubkeyLength = 32
	// MaxAddressLength bounds a stored account address, in bytes.
	MaxAddressLength = 128
)

// ValidateTier checks that the tier is one this chain knows.
func ValidateTier(tier uint64) error {
	if tier != TierEntry && tier != TierExit {
		return errorsmod.Wrapf(ErrInvalidTier, "got %d, want %d (entry) or %d (exit)", tier, TierEntry, TierExit)
	}
	return nil
}

// ValidateEndpoint checks that the endpoint is "host:port" with a port in
// 1-65535. It parses text only and never touches the network, so every
// validator reaches the same answer.
func ValidateEndpoint(endpoint string) error {
	if len(endpoint) == 0 || len(endpoint) > MaxEndpointLength {
		return errorsmod.Wrapf(ErrInvalidEndpoint, "length %d, want 1-%d", len(endpoint), MaxEndpointLength)
	}
	for i := 0; i < len(endpoint); i++ {
		// printable ASCII only: no spaces, control bytes or non-ASCII
		if endpoint[i] <= 0x20 || endpoint[i] >= 0x7f {
			return errorsmod.Wrap(ErrInvalidEndpoint, "only printable ASCII without spaces is allowed")
		}
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return errorsmod.Wrap(ErrInvalidEndpoint, "want host:port")
	}
	if host == "" {
		return errorsmod.Wrap(ErrInvalidEndpoint, "empty host")
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil || p == 0 {
		return errorsmod.Wrap(ErrInvalidEndpoint, "port must be 1-65535")
	}
	return nil
}

// ValidatePubkey checks that the key is standard base64 of exactly 32 bytes,
// the format WireGuard uses for public keys.
func ValidatePubkey(pubkey string) error {
	bz, err := base64.StdEncoding.DecodeString(pubkey)
	if err != nil {
		return errorsmod.Wrap(ErrInvalidPubkey, "not valid base64")
	}
	if len(bz) != PubkeyLength {
		return errorsmod.Wrapf(ErrInvalidPubkey, "decoded length %d, want %d", len(bz), PubkeyLength)
	}
	// reject non-canonical encodings so one key has exactly one text form
	if base64.StdEncoding.EncodeToString(bz) != pubkey {
		return errorsmod.Wrap(ErrInvalidPubkey, "non-canonical base64")
	}
	return nil
}

// ValidateBasic checks a stored node for internal consistency. It is used
// when importing genesis.
func (n Node) ValidateBasic() error {
	if n.Operator == "" {
		return errorsmod.Wrap(ErrInvalidNode, "empty operator")
	}
	if err := ValidateTier(n.Tier); err != nil {
		return err
	}
	if n.Commitment != "" {
		// A bridge publishes nothing that would let a censor find it or
		// confirm a guess.
		if err := ValidateCommitment(n.Commitment); err != nil {
			return err
		}
		if n.Endpoint != "" || n.Pubkey != "" || n.Reality != nil || n.Tier != TierEntry {
			return errorsmod.Wrap(ErrIsBridge, "a bridge is an entry node with no endpoint, pubkey or disguise on the chain")
		}
	} else {
		if err := ValidateEndpoint(n.Endpoint); err != nil {
			return err
		}
		if err := ValidatePubkey(n.Pubkey); err != nil {
			return err
		}
	}
	if err := n.Bond.Validate(); err != nil {
		return errorsmod.Wrap(ErrInvalidBond, err.Error())
	}
	if n.ServiceKey != "" && (n.ServiceKey == n.Operator || len(n.ServiceKey) > MaxAddressLength) {
		return errorsmod.Wrap(ErrInvalidServiceKey, "must differ from the operator and be a valid address")
	}
	if n.Reality != nil {
		if n.Tier != TierEntry {
			return errorsmod.Wrap(ErrInvalidReality, "only entry nodes offer a disguise")
		}
		if err := ValidateReality(*n.Reality); err != nil {
			return err
		}
	}
	// The sponsored part is part of the bond, never more than it.
	if n.Bond.Amount.IsNil() || math.NewIntFromUint64(n.Sponsored).GT(n.Bond.Amount) {
		return errorsmod.Wrap(ErrInvalidBond, "sponsored amount is larger than the bond")
	}
	switch n.Status {
	case StatusActive:
		if n.UnbondingEnd != 0 {
			return errorsmod.Wrap(ErrInvalidNode, "active node has an unbonding end time")
		}
	case StatusUnbonding:
		if n.UnbondingEnd <= 0 {
			return errorsmod.Wrap(ErrInvalidNode, "unbonding node has no unbonding end time")
		}
	default:
		return errorsmod.Wrapf(ErrInvalidNode, "unknown status %d", n.Status)
	}
	return nil
}
