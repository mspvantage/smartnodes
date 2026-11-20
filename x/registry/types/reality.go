package types

import (
	"encoding/base64"
	"regexp"

	errorsmod "cosmossdk.io/errors"
)

var (
	realityShortIDRe = regexp.MustCompile(`^([0-9a-f]{2}){0,8}$`)
	realityUUIDRe    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	realityNameRe    = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// ValidateReality checks a published disguise, in the same way devices and
// the node program do, so a value the chain accepts always works for them.
func ValidateReality(r Reality) error {
	if r.Port == 0 || r.Port > 65535 {
		return errorsmod.Wrapf(ErrInvalidReality, "port %d", r.Port)
	}
	if k, err := base64.RawURLEncoding.DecodeString(r.PublicKey); err != nil || len(k) != 32 {
		return errorsmod.Wrap(ErrInvalidReality, "public key must be 32 bytes, base64url without padding")
	}
	if !realityShortIDRe.MatchString(r.ShortId) {
		return errorsmod.Wrap(ErrInvalidReality, "short id must be up to 16 lowercase hex characters, in pairs")
	}
	if len(r.ServerName) > 253 || !realityNameRe.MatchString(r.ServerName) {
		return errorsmod.Wrapf(ErrInvalidReality, "%q is not a host name", r.ServerName)
	}
	if !realityUUIDRe.MatchString(r.Uuid) {
		return errorsmod.Wrap(ErrInvalidReality, "uuid must be in the lowercase 8-4-4-4-12 form")
	}
	return nil
}
