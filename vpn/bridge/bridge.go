// Package bridge is the bridge line: everything a device needs to reach an
// unlisted entry node, on one line its operator can hand to people.
//
//	smartnodes-bridge:CHAIN/NODE/ADDRESS/PORT/WGKEY/RPORT/RKEY/SHORTID/NAME/UUID
//
// ADDRESS is where the bridge's disguise listens, on RPORT. PORT is the
// node's own WireGuard and payment port, which only the disguise reaches.
// WGKEY is the node's WireGuard key in base64url. The rest is its REALITY
// disguise.
//
// The chain keeps only the line's fingerprint, so a device can check a line
// belongs to a bonded node, and nobody reading the chain learns any of it.
// The node id is left out of the fingerprint, because the operator needs the
// fingerprint to register, before the chain has given the node an id.
package bridge

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/mspvantage/smartnodes/vpn/disguise"
)

const (
	prefix = "smartnodes-bridge:"
	domain = "SMARTNODES-BRIDGE-V1\n"
)

// Line is a bridge line.
type Line struct {
	ChainID string
	NodeID  uint64
	// Address is where the disguise listens, at Reality.Port.
	Address netip.Addr
	// Port is the node's WireGuard and payment port, reached only through
	// the disguise, on the node's loopback address.
	Port uint16
	// WGKey is the node's WireGuard public key, in standard base64 as
	// WireGuard writes it.
	WGKey   string
	Reality disguise.Reality
}

// Validate checks every part.
func (l Line) Validate() error {
	if l.ChainID == "" || strings.ContainsAny(l.ChainID, "/: \t\n") {
		return errors.New("bridge: bad chain id")
	}
	if !l.Address.IsValid() || !l.Address.Is4() || l.Address.IsUnspecified() || l.Address.IsLoopback() {
		return errors.New("bridge: the address must be the bridge's own IPv4 address")
	}
	if l.Port == 0 {
		return errors.New("bridge: no node port")
	}
	if k, err := base64.StdEncoding.DecodeString(l.WGKey); err != nil || len(k) != 32 {
		return errors.New("bridge: bad WireGuard key")
	}
	return l.Reality.Validate()
}

func (l Line) parts(id string) string {
	key, _ := base64.StdEncoding.DecodeString(l.WGKey)
	return strings.Join([]string{l.ChainID, id, l.Address.String(), strconv.Itoa(int(l.Port)),
		base64.RawURLEncoding.EncodeToString(key), l.Reality.Compact()}, "/")
}

// String writes the line.
func (l Line) String() string { return prefix + l.parts(strconv.FormatUint(l.NodeID, 10)) }

// Fingerprint is what the chain keeps: SHA-256 of the line without its node
// id, in lowercase hex.
func (l Line) Fingerprint() string {
	sum := sha256.Sum256([]byte(domain + l.parts("")))
	return hex.EncodeToString(sum[:])
}

// Endpoint is where devices connect.
func (l Line) Endpoint() netip.AddrPort { return netip.AddrPortFrom(l.Address, l.Reality.Port) }

// Inner is the node's own port, as the disguise reaches it on the node.
func (l Line) Inner() netip.AddrPort {
	return netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), l.Port)
}

// Parse reads a line.
func Parse(s string) (Line, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, prefix) {
		return Line{}, errors.New("bridge: a bridge line starts with " + prefix)
	}
	p := strings.Split(strings.TrimPrefix(s, prefix), "/")
	if len(p) != 10 {
		return Line{}, fmt.Errorf("bridge: %d parts, want 10", len(p))
	}
	id, err := strconv.ParseUint(p[1], 10, 64)
	if err != nil {
		return Line{}, errors.New("bridge: bad node id")
	}
	addr, err := netip.ParseAddr(p[2])
	if err != nil {
		return Line{}, errors.New("bridge: bad address")
	}
	port, err := strconv.ParseUint(p[3], 10, 16)
	if err != nil {
		return Line{}, errors.New("bridge: bad port")
	}
	key, err := base64.RawURLEncoding.DecodeString(p[4])
	if err != nil || len(key) != 32 {
		return Line{}, errors.New("bridge: bad WireGuard key")
	}
	r, err := disguise.ParseCompact(strings.Join(p[5:], "/"))
	if err != nil {
		return Line{}, err
	}
	l := Line{ChainID: p[0], NodeID: id, Address: addr, Port: uint16(port), WGKey: base64.StdEncoding.EncodeToString(key), Reality: r}
	return l, l.Validate()
}
