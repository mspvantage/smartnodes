// Package disguise hides the connection between a device and its entry node
// from censors, using Xray's REALITY.
//
// To anyone watching, and to a censor that connects to the node to test it,
// the node looks like an ordinary secure website: a real one, the "cover"
// site, whose handshake the node passes along. Only a device that knows the
// node's REALITY public key and short id gets through, into a channel that
// carries the WireGuard tunnel and the payment connection, nothing else.
//
// Only the entry hop needs this: the exit hop already travels inside the
// entry tunnel. The node and the device each run the Xray program, which
// this package configures; Xray keeps no logs in either configuration.
package disguise

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Reality is what a node publishes about its disguise, so devices can use
// it. None of it is secret: the private key stays on the node.
type Reality struct {
	// Port is the TCP port the disguise listens on, usually 443.
	Port uint16 `json:"port"`
	// PublicKey is the node's REALITY public key, base64url without padding.
	PublicKey string `json:"public_key"`
	// ShortID is a hex string of up to 16 characters.
	ShortID string `json:"short_id"`
	// ServerName is the cover site's name, which devices present.
	ServerName string `json:"server_name"`
	// UUID is the VLESS user id. It is published, so it grants nothing:
	// tickets pay for service, as always.
	UUID string `json:"uuid"`
}

var (
	shortIDRe    = regexp.MustCompile(`^([0-9a-f]{2}){0,8}$`)
	uuidRe       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	serverNameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// Validate checks a published disguise.
func (r Reality) Validate() error {
	if r.Port == 0 {
		return errors.New("disguise: no port")
	}
	if k, err := base64.RawURLEncoding.DecodeString(r.PublicKey); err != nil || len(k) != 32 {
		return errors.New("disguise: public key must be 32 bytes, base64url without padding")
	}
	if !shortIDRe.MatchString(r.ShortID) {
		return errors.New("disguise: short id must be up to 16 lowercase hex characters, in pairs")
	}
	if len(r.ServerName) > 253 || !serverNameRe.MatchString(r.ServerName) {
		return fmt.Errorf("disguise: %q is not a host name", r.ServerName)
	}
	if !uuidRe.MatchString(r.UUID) {
		return errors.New("disguise: uuid must be in the usual lowercase 8-4-4-4-12 form")
	}
	return nil
}

// ServerKeys are a node's disguise secrets and their public parts. The node
// keeps them in a file, so what it publishes stays valid across restarts.
type ServerKeys struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
	UUID       string `json:"uuid"`
}

// NewServerKeys makes fresh keys.
func NewServerKeys() (ServerKeys, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return ServerKeys{}, err
	}
	sid := make([]byte, 8)
	u := make([]byte, 16)
	if _, err := rand.Read(sid); err != nil {
		return ServerKeys{}, err
	}
	if _, err := rand.Read(u); err != nil {
		return ServerKeys{}, err
	}
	u[6] = u[6]&0x0f | 0x40 // version 4
	u[8] = u[8]&0x3f | 0x80 // variant
	h := hex.EncodeToString(u)
	return ServerKeys{
		PrivateKey: base64.RawURLEncoding.EncodeToString(priv.Bytes()),
		PublicKey:  base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		ShortID:    hex.EncodeToString(sid),
		UUID:       h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32],
	}, nil
}

// Check confirms the public key belongs to the private key.
func (k ServerKeys) Check() error {
	raw, err := base64.RawURLEncoding.DecodeString(k.PrivateKey)
	if err != nil {
		return errors.New("disguise: bad private key")
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return err
	}
	if base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()) != k.PublicKey {
		return errors.New("disguise: public key does not match the private key")
	}
	return nil
}

// Published is what the node announces for these keys.
func (k ServerKeys) Published(port uint16, serverName string) Reality {
	return Reality{Port: port, PublicKey: k.PublicKey, ShortID: k.ShortID, ServerName: serverName, UUID: k.UUID}
}

// flow is VLESS's Vision mode, which hides the size pattern of a handshake
// carried inside another; Xray's recommended pairing with REALITY.
const flow = "xtls-rprx-vision"

// noLogs turns off Xray's access and error logs and its name lookup log.
var noLogs = map[string]any{"access": "none", "error": "none", "loglevel": "none", "dnsLog": false}

// ServerConfig is the node's Xray configuration. It listens on listen,
// passes the cover site's handshake to cover (host:port) for anyone without
// the key, and lets key holders reach only allow: the node's own WireGuard
// and payment port. Everything else is refused, so the disguise can never
// be used as an open proxy.
func ServerConfig(k ServerKeys, listen netip.AddrPort, cover, serverName string, allow netip.AddrPort) ([]byte, error) {
	if err := k.Check(); err != nil {
		return nil, err
	}
	if err := k.Published(listen.Port(), serverName).Validate(); err != nil {
		return nil, err
	}
	if !allow.IsValid() || !allow.Addr().Is4() {
		return nil, errors.New("disguise: the allowed address must be IPv4")
	}
	cfg := map[string]any{
		"log": noLogs,
		"inbounds": []any{map[string]any{
			"tag": "in", "listen": listen.Addr().String(), "port": listen.Port(), "protocol": "vless",
			"settings": map[string]any{
				"clients":    []any{map[string]any{"id": k.UUID, "flow": flow}},
				"decryption": "none",
			},
			"streamSettings": map[string]any{
				"network": "tcp", "security": "reality",
				"realitySettings": map[string]any{
					"show": false, "dest": cover, "xver": 0,
					"serverNames": []string{serverName},
					"privateKey":  k.PrivateKey,
					"shortIds":    []string{k.ShortID},
				},
			},
		}},
		"outbounds": []any{
			// Xray's own firewall says the same again: only the node's port.
			// Xray refuses private addresses by default; the node's own
			// address may be one, so it is allowed by name. ("finalRules"
			// is the name current releases read; later ones accept it too.)
			map[string]any{"tag": "node", "protocol": "freedom", "settings": map[string]any{
				"finalRules": []any{
					map[string]any{"action": "allow", "network": []string{"tcp", "udp"}, "ip": []string{allow.Addr().String() + "/32"}, "port": strconv.Itoa(int(allow.Port()))},
					map[string]any{"action": "block", "network": []string{"tcp", "udp"}},
				},
			}},
			map[string]any{"tag": "refuse", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"domainStrategy": "AsIs",
			"rules": []any{
				map[string]any{"type": "field", "ip": []string{allow.Addr().String() + "/32"}, "port": strconv.Itoa(int(allow.Port())), "outboundTag": "node"},
				map[string]any{"type": "field", "network": "tcp,udp", "outboundTag": "refuse"},
			},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// ClientConfig is the device's Xray configuration. It listens on local
// (TCP and UDP, on the loopback address) and carries what arrives, through
// the disguise at entryIP, to target: the entry node's WireGuard and payment
// port. The device then points WireGuard and the payment connection at
// local instead of at the node.
func ClientConfig(r Reality, entryIP netip.Addr, local, target netip.AddrPort) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if !local.Addr().IsLoopback() {
		return nil, errors.New("disguise: the local side must be on the loopback address")
	}
	cfg := map[string]any{
		"log": noLogs,
		"inbounds": []any{map[string]any{
			"tag": "local", "listen": local.Addr().String(), "port": local.Port(), "protocol": "dokodemo-door",
			"settings": map[string]any{"address": target.Addr().String(), "port": target.Port(), "network": "tcp,udp"},
		}},
		"outbounds": []any{map[string]any{
			"tag": "entry", "protocol": "vless",
			"settings": map[string]any{"vnext": []any{map[string]any{
				"address": entryIP.String(), "port": r.Port,
				"users": []any{map[string]any{"id": r.UUID, "encryption": "none", "flow": flow}},
			}}},
			"streamSettings": map[string]any{
				"network": "tcp", "security": "reality",
				"realitySettings": map[string]any{
					// Look like a current Chrome browser.
					"fingerprint": "chrome", "serverName": r.ServerName,
					"publicKey": r.PublicKey, "shortId": r.ShortID, "spiderX": "/",
				},
			},
		}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// Compact writes a disguise on one line, PORT/PUBLICKEY/SHORTID/SERVERNAME/UUID,
// for passing on a command line. None of the parts can contain a slash.
func (r Reality) Compact() string {
	return fmt.Sprintf("%d/%s/%s/%s/%s", r.Port, r.PublicKey, r.ShortID, r.ServerName, r.UUID)
}

// ParseCompact reads what Compact writes.
func ParseCompact(s string) (Reality, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 5 {
		return Reality{}, errors.New("disguise: want PORT/PUBLICKEY/SHORTID/SERVERNAME/UUID")
	}
	port, err := strconv.ParseUint(parts[0], 10, 16)
	if err != nil {
		return Reality{}, fmt.Errorf("disguise: port %q", parts[0])
	}
	r := Reality{Port: uint16(port), PublicKey: parts[1], ShortID: parts[2], ServerName: parts[3], UUID: parts[4]}
	return r, r.Validate()
}
