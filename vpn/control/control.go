// Package control is the short, encrypted exchange in which a SmartNodes
// client pays a node and the node opens a WireGuard session for it.
//
// The exchange uses the Noise IK handshake, the same pattern and algorithms
// WireGuard uses (Curve25519, ChaCha20-Poly1305, BLAKE2s). The client knows
// the node's public key from the chain registry, so it can be sure it is
// talking to the registered node. The client's own key is created fresh for
// every exchange and identifies nothing.
//
// A Hello either pays for a session now, pays ahead for a later one (an exit
// credit, with Prepay set), or opens a session from a credit bought earlier
// (no tickets). Paying an exit ahead keeps the moment its ticket is redeemed
// unrelated to the moment the session opens, so the chain cannot be used to
// pair an entry node with an exit node.
//
// One exchange is two messages over TCP:
//
//	client -> node   handshake message 1, carrying a Hello (tickets and the
//	                 session's WireGuard key), encrypted to the node's key
//	node -> client   handshake message 2, carrying a Reply
//
// Each message is sent as a two-byte length followed by the message.
package control

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/flynn/noise"
)

const (
	// Version is the protocol version a Hello carries.
	Version = 1
	// MaxFrame bounds one message on the wire. A Hello with its maximum
	// number of tickets is far smaller.
	MaxFrame = 16 * 1024
	// MaxTickets bounds the tickets in one Hello.
	MaxTickets = 8
	// Timeout bounds the whole exchange. It includes the node waiting for
	// the chain to accept the tickets, about one block.
	Timeout = 30 * time.Second
	// KeySize is the size of a Curve25519 key.
	KeySize = 32
)

var (
	// ErrFrameTooLarge is returned for a message over MaxFrame.
	ErrFrameTooLarge = errors.New("control: message too large")
	// ErrBadHello is returned for a Hello that cannot be read.
	ErrBadHello = errors.New("control: malformed hello")
)

// cipherSuite matches WireGuard's choice of algorithms.
var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)

// prologue binds the exchange to one chain and one node. A message recorded
// for one node cannot be replayed to another.
func prologue(chainID string, nodeID uint64) []byte {
	return []byte("SMARTNODES-CONTROL-V1/" + chainID + "/" + strconv.FormatUint(nodeID, 10))
}

// Hello is what the client sends.
type Hello struct {
	Version int `json:"version"`
	// KeyID is the issuer key the tickets were signed under.
	KeyID uint64 `json:"key_id"`
	// Tickets are spent tickets naming this node, in standard base64.
	Tickets []string `json:"tickets"`
	// WGPubkey is the session's WireGuard public key, in standard base64.
	// With Prepay, it is the key the credit is held for.
	WGPubkey string `json:"wg_pubkey"`
	// Prepay asks the node to hold the tickets as credit for a session that
	// opens later with WGPubkey, instead of opening one now.
	Prepay bool `json:"prepay,omitempty"`
	// Close ends the session for WGPubkey now, so the node can give its
	// place to someone else. It carries no tickets.
	Close bool `json:"close,omitempty"`

	// Op asks for a bridge-line service instead of a session; see the Op
	// constants. It carries no WireGuard key.
	Op string `json:"op,omitempty"`
	// RequestKey is the device's one-time key for a bridge request, base64.
	RequestKey string `json:"request_key,omitempty"`
	// NodeID names the bridge whose fingerprint is asked for.
	NodeID uint64 `json:"node_id,omitempty"`
}

// Bridge-line services a node offers its clients, so that a device never
// needs to reach the chain itself, and no account of the device's ever
// appears on it.
const (
	// OpBridgeRequest spends one ticket made out to the bridge pool for one
	// bridge line, sealed to RequestKey. The node sends it to the chain.
	OpBridgeRequest = "bridge-request"
	// OpBridgeAnswer collects the sealed answer for RequestKey.
	OpBridgeAnswer = "bridge-answer"
	// OpBridgeFingerprint reads bridge NodeID's fingerprint from the chain.
	OpBridgeFingerprint = "bridge-fingerprint"
)

// Reply is what the node answers.
type Reply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// TunnelIP is the client's address inside the tunnel.
	TunnelIP string `json:"tunnel_ip,omitempty"`
	// Expires is when the session ends unless renewed, in Unix seconds. For
	// a prepayment, it is when the credit lapses if unused.
	Expires int64 `json:"expires,omitempty"`
	// DNS is a name server to use inside the tunnel, if the node runs one.
	DNS string `json:"dns,omitempty"`

	// Sealed and State answer OpBridgeAnswer: the sealed line, base64, and
	// the request's state (1 open, 2 answered, 3 lapsed).
	Sealed string `json:"sealed,omitempty"`
	State  uint32 `json:"state,omitempty"`
	// Commitment answers OpBridgeFingerprint.
	Commitment string `json:"commitment,omitempty"`
}

// KeyFromPrivate builds a key pair from a raw Curve25519 private key, such
// as one made by "wg genkey".
func KeyFromPrivate(private []byte) (noise.DHKey, error) {
	if len(private) != KeySize {
		return noise.DHKey{}, fmt.Errorf("control: private key is %d bytes, want %d", len(private), KeySize)
	}
	priv, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil {
		return noise.DHKey{}, err
	}
	return noise.DHKey{Private: append([]byte(nil), private...), Public: priv.PublicKey().Bytes()}, nil
}

// KeyFromBase64 reads a private key in WireGuard's base64 format.
func KeyFromBase64(text string) (noise.DHKey, error) {
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return noise.DHKey{}, fmt.Errorf("control: private key is not base64: %w", err)
	}
	return KeyFromPrivate(raw)
}

// DecodePublicKey reads a public key in WireGuard's base64 format.
func DecodePublicKey(text string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("control: public key is not base64: %w", err)
	}
	if len(raw) != KeySize {
		return nil, fmt.Errorf("control: public key is %d bytes, want %d", len(raw), KeySize)
	}
	return raw, nil
}

// NewKey makes a fresh random key pair.
func NewKey() (noise.DHKey, error) {
	return noise.DH25519.GenerateKeypair(rand.Reader)
}

func writeFrame(w io.Writer, msg []byte) error {
	if len(msg) > MaxFrame {
		return ErrFrameTooLarge
	}
	var head [2]byte
	binary.BigEndian.PutUint16(head[:], uint16(len(msg)))
	if _, err := w.Write(append(head[:], msg...)); err != nil {
		return err
	}
	return nil
}

func readFrame(r io.Reader) ([]byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(head[:]))
	if n > MaxFrame {
		return nil, ErrFrameTooLarge
	}
	msg := make([]byte, n)
	if _, err := io.ReadFull(r, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// Dial runs the client side of one exchange over conn. nodeKey is the node's
// public key from the registry. If the other side does not hold the matching
// private key, Dial fails and nothing is revealed to it: the Hello is
// encrypted to the registered key.
func Dial(conn net.Conn, chainID string, nodeID uint64, nodeKey []byte, hello Hello) (Reply, error) {
	if len(nodeKey) != KeySize {
		return Reply{}, fmt.Errorf("control: node key is %d bytes, want %d", len(nodeKey), KeySize)
	}
	_ = conn.SetDeadline(time.Now().Add(Timeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	// A fresh key for every exchange, so the node cannot tell two
	// exchanges from the same device apart by key.
	own, err := NewKey()
	if err != nil {
		return Reply{}, err
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cipherSuite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeIK,
		Initiator:     true,
		Prologue:      prologue(chainID, nodeID),
		StaticKeypair: own,
		PeerStatic:    nodeKey,
	})
	if err != nil {
		return Reply{}, err
	}

	hello.Version = Version
	payload, err := json.Marshal(hello)
	if err != nil {
		return Reply{}, err
	}
	msg1, _, _, err := hs.WriteMessage(nil, payload)
	if err != nil {
		return Reply{}, err
	}
	if err := writeFrame(conn, msg1); err != nil {
		return Reply{}, err
	}

	msg2, err := readFrame(conn)
	if err != nil {
		return Reply{}, fmt.Errorf("control: no answer from node: %w", err)
	}
	body, _, _, err := hs.ReadMessage(nil, msg2)
	if err != nil {
		// Only the holder of the registered key can produce this message.
		return Reply{}, fmt.Errorf("control: node failed to prove its identity: %w", err)
	}
	var reply Reply
	if err := json.Unmarshal(body, &reply); err != nil {
		return Reply{}, fmt.Errorf("control: unreadable answer: %w", err)
	}
	return reply, nil
}

// Handler decides the answer to a Hello. It is called only after the Hello
// has been decrypted and parsed.
type Handler func(Hello) Reply

// Serve runs the node side of one exchange over conn.
func Serve(conn net.Conn, chainID string, nodeID uint64, key noise.DHKey, handle Handler) error {
	_ = conn.SetDeadline(time.Now().Add(Timeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cipherSuite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeIK,
		Initiator:     false,
		Prologue:      prologue(chainID, nodeID),
		StaticKeypair: key,
	})
	if err != nil {
		return err
	}

	msg1, err := readFrame(conn)
	if err != nil {
		return err
	}
	payload, _, _, err := hs.ReadMessage(nil, msg1)
	if err != nil {
		return fmt.Errorf("control: handshake failed: %w", err)
	}
	hello, err := parseHello(payload)

	var reply Reply
	if err != nil {
		reply = Reply{Error: err.Error()}
	} else {
		reply = handle(hello)
	}

	body, err := json.Marshal(reply)
	if err != nil {
		return err
	}
	msg2, _, _, err := hs.WriteMessage(nil, body)
	if err != nil {
		return err
	}
	return writeFrame(conn, msg2)
}

func parseHello(payload []byte) (Hello, error) {
	var hello Hello
	if err := json.Unmarshal(payload, &hello); err != nil {
		return Hello{}, ErrBadHello
	}
	if hello.Version != Version {
		return Hello{}, fmt.Errorf("control: unsupported version %d", hello.Version)
	}
	if len(hello.Tickets) > MaxTickets {
		return Hello{}, fmt.Errorf("control: %d tickets, want at most %d", len(hello.Tickets), MaxTickets)
	}
	if hello.Prepay && len(hello.Tickets) == 0 {
		return Hello{}, errors.New("control: a prepayment needs tickets")
	}
	if hello.Close && (hello.Prepay || len(hello.Tickets) > 0) {
		return Hello{}, errors.New("control: a close carries no tickets")
	}
	switch hello.Op {
	case "":
	case OpBridgeRequest, OpBridgeAnswer:
		if k, err := base64.StdEncoding.DecodeString(hello.RequestKey); err != nil || len(k) != 32 {
			return Hello{}, errors.New("control: a bridge request needs a 32-byte request key")
		}
		want := 0
		if hello.Op == OpBridgeRequest {
			want = 1
		}
		if len(hello.Tickets) != want || hello.Prepay || hello.Close {
			return Hello{}, fmt.Errorf("control: %s carries %d ticket(s) and nothing else", hello.Op, want)
		}
		return hello, nil
	case OpBridgeFingerprint:
		if len(hello.Tickets) != 0 || hello.Prepay || hello.Close {
			return Hello{}, errors.New("control: a fingerprint question carries nothing else")
		}
		return hello, nil
	default:
		return Hello{}, fmt.Errorf("control: unknown op %q", hello.Op)
	}
	if _, err := DecodePublicKey(hello.WGPubkey); err != nil {
		return Hello{}, err
	}
	return hello, nil
}
