// Package crypto implements the SmartNodes ticket scheme: blind BLS
// signatures on BLS12-381 with tickets identified by one-time Ed25519 keys.
//
// The protocol is described in "SmartNodes: Ticket and Escrow Protocol".
// In short:
//
//	M  = HashToG1(chain id, key id, ticket key)   the ticket as a curve point
//	B  = r * M                                    blinded by the buyer
//	S' = s * B                                    signed by the issuer
//	S  = r^-1 * S' = s * M                        unblinded by the buyer
//	e(S, G2) == e(M, P)                           checked by anyone, P = s * G2
//
// Signatures and blinded values live in G1 (48 bytes compressed). The issuer
// public key lives in G2 (96 bytes compressed).
//
// THIS CODE HAS NOT BEEN REVIEWED BY AN INDEPENDENT CRYPTOGRAPHER. It must not
// protect real value until it has been.
package crypto

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	blst "github.com/supranational/blst/bindings/go"
)

// Sizes of the encoded values, in bytes.
const (
	G1Size        = 48 // blinded values and signatures
	PublicKeySize = 96 // issuer public key
	SecretKeySize = 32 // issuer secret key and blinding factors
	TicketKeySize = ed25519.PublicKeySize
	SpendSigSize  = ed25519.SignatureSize
	// TicketSize is a ticket as presented for redemption:
	// ticket key || issuer signature || spend signature.
	TicketSize = TicketKeySize + G1Size + SpendSigSize
)

// hashDST is the hash-to-curve domain separation tag. It names the protocol,
// its version and the standard suite, as RFC 9380 recommends.
var hashDST = []byte("SMARTNODES-TICKET-V1_BLS12381G1_XMD:SHA-256_SSWU_RO_")

// spendTag prefixes the statement a ticket holder signs to pay a node.
const spendTag = "SMARTNODES-TICKET-SPEND-V1"

// Errors returned by this package.
var (
	ErrInvalidPublicKey = errors.New("invalid issuer public key")
	ErrInvalidPoint     = errors.New("invalid curve point")
	ErrInvalidSecret    = errors.New("invalid secret value")
	ErrInvalidTicket    = errors.New("invalid ticket encoding")
	ErrBadBlindSig      = errors.New("blind signature does not verify")
	ErrBadIssuerSig     = errors.New("issuer signature does not verify")
	ErrBadSpendSig      = errors.New("spend signature does not verify")
)

// g2Generator is the fixed generator of G2 in affine form.
var g2Generator = blst.P2Generator().ToAffine()

// ---------------------------------------------------------------------------
// Encoding helpers
// ---------------------------------------------------------------------------

// appendLenPrefixed appends a 2-byte length followed by the bytes, so that
// two different (chain id, ...) tuples can never produce the same message.
func appendLenPrefixed(dst []byte, b []byte) []byte {
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(b)))
	return append(dst, b...)
}

// ticketMessage is the byte string hashed to the curve for a ticket. It binds
// the ticket to one chain and one issuer key period.
func ticketMessage(chainID string, keyID uint64, ticketKey []byte) []byte {
	msg := make([]byte, 0, 2+len(chainID)+8+len(ticketKey))
	msg = appendLenPrefixed(msg, []byte(chainID))
	msg = binary.BigEndian.AppendUint64(msg, keyID)
	return append(msg, ticketKey...)
}

// SpendMessage is the statement a ticket holder signs to pay a ticket to one
// node. It binds the payment to a chain, an issuer key, a node and the ticket.
func SpendMessage(chainID string, keyID, nodeID uint64, ticketKey []byte) []byte {
	msg := make([]byte, 0, len(spendTag)+2+len(chainID)+16+len(ticketKey))
	msg = append(msg, spendTag...)
	msg = appendLenPrefixed(msg, []byte(chainID))
	msg = binary.BigEndian.AppendUint64(msg, keyID)
	msg = binary.BigEndian.AppendUint64(msg, nodeID)
	return append(msg, ticketKey...)
}

// decodeG1 parses a compressed G1 point and rejects anything that is not a
// valid, non-identity member of the prime-order subgroup.
func decodeG1(b []byte) (*blst.P1Affine, error) {
	if len(b) != G1Size {
		return nil, fmt.Errorf("%w: length %d, want %d", ErrInvalidPoint, len(b), G1Size)
	}
	p := new(blst.P1Affine).Uncompress(b)
	if p == nil {
		return nil, fmt.Errorf("%w: not a point on the curve", ErrInvalidPoint)
	}
	// SigValidate(true) rejects the identity and points outside the subgroup.
	if !p.SigValidate(true) {
		return nil, fmt.Errorf("%w: identity or outside the subgroup", ErrInvalidPoint)
	}
	return p, nil
}

// decodePublicKey parses a compressed G2 point under the same rules.
func decodePublicKey(b []byte) (*blst.P2Affine, error) {
	if len(b) != PublicKeySize {
		return nil, fmt.Errorf("%w: length %d, want %d", ErrInvalidPublicKey, len(b), PublicKeySize)
	}
	p := new(blst.P2Affine).Uncompress(b)
	if p == nil {
		return nil, fmt.Errorf("%w: not a point on the curve", ErrInvalidPublicKey)
	}
	// KeyValidate rejects the identity and points outside the subgroup.
	if !p.KeyValidate() {
		return nil, fmt.Errorf("%w: identity or outside the subgroup", ErrInvalidPublicKey)
	}
	return p, nil
}

func decodeScalar(b []byte) (*blst.Scalar, error) {
	if len(b) != SecretKeySize {
		return nil, fmt.Errorf("%w: length %d, want %d", ErrInvalidSecret, len(b), SecretKeySize)
	}
	s := new(blst.Scalar).Deserialize(b)
	if s == nil || !s.Valid() {
		return nil, fmt.Errorf("%w: zero or out of range", ErrInvalidSecret)
	}
	return s, nil
}

// pairingEqual reports whether e(a, G2) == e(b, pk).
func pairingEqual(a *blst.P1Affine, b *blst.P1Affine, pk *blst.P2Affine) bool {
	left := blst.Fp12MillerLoop(g2Generator, a)
	right := blst.Fp12MillerLoop(pk, b)
	return blst.Fp12FinalVerify(left, right)
}

// ---------------------------------------------------------------------------
// Checks the chain performs
// ---------------------------------------------------------------------------

// ValidatePublicKey checks an issuer public key before it is stored.
func ValidatePublicKey(pk []byte) error {
	_, err := decodePublicKey(pk)
	return err
}

// ValidateBlinded checks a blinded ticket before the chain accepts payment
// for it.
func ValidateBlinded(blinded []byte) error {
	_, err := decodeG1(blinded)
	return err
}

// VerifyBlindSignature checks that blindSig is the issuer's signature on the
// blinded value: e(S', G2) == e(B, P). It reveals nothing about the ticket.
func VerifyBlindSignature(pk, blinded, blindSig []byte) error {
	p, err := decodePublicKey(pk)
	if err != nil {
		return err
	}
	b, err := decodeG1(blinded)
	if err != nil {
		return err
	}
	s, err := decodeG1(blindSig)
	if err != nil {
		return err
	}
	if !pairingEqual(s, b, p) {
		return ErrBadBlindSig
	}
	return nil
}

// VerifyTicket checks a ticket presented for redemption by nodeID. It
// verifies the holder's spend signature and the issuer's signature, and
// returns the ticket key, which the chain records as spent.
func VerifyTicket(pk []byte, chainID string, keyID, nodeID uint64, ticket []byte) ([]byte, error) {
	if len(ticket) != TicketSize {
		return nil, fmt.Errorf("%w: length %d, want %d", ErrInvalidTicket, len(ticket), TicketSize)
	}
	ticketKey := ticket[:TicketKeySize]
	issuerSig := ticket[TicketKeySize : TicketKeySize+G1Size]
	spendSig := ticket[TicketKeySize+G1Size:]

	// The cheap check first: the holder authorised payment to this node.
	if !ed25519.Verify(ed25519.PublicKey(ticketKey), SpendMessage(chainID, keyID, nodeID, ticketKey), spendSig) {
		return nil, ErrBadSpendSig
	}

	p, err := decodePublicKey(pk)
	if err != nil {
		return nil, err
	}
	s, err := decodeG1(issuerSig)
	if err != nil {
		return nil, err
	}
	m := blst.HashToG1(ticketMessage(chainID, keyID, ticketKey), hashDST).ToAffine()
	if !pairingEqual(s, m, p) {
		return nil, ErrBadIssuerSig
	}

	out := make([]byte, TicketKeySize)
	copy(out, ticketKey)
	return out, nil
}

// ---------------------------------------------------------------------------
// Issuer side
// ---------------------------------------------------------------------------

// GenerateIssuerKey derives an issuer key pair from at least 32 bytes of
// secret random input.
func GenerateIssuerKey(seed []byte) (secretKey, publicKey []byte, err error) {
	if len(seed) < 32 {
		return nil, nil, fmt.Errorf("%w: seed must be at least 32 bytes", ErrInvalidSecret)
	}
	sk := blst.KeyGen(seed, []byte("SMARTNODES-TICKET-ISSUER-V1"))
	if sk == nil {
		return nil, nil, ErrInvalidSecret
	}
	pk := new(blst.P2Affine).From(sk)
	return sk.Serialize(), pk.Compress(), nil
}

// BlindSign signs a blinded ticket with the issuer secret key: S' = s * B.
func BlindSign(secretKey, blinded []byte) ([]byte, error) {
	sk, err := decodeScalar(secretKey)
	if err != nil {
		return nil, err
	}
	b, err := decodeG1(blinded)
	if err != nil {
		return nil, err
	}
	var point blst.P1
	point.FromAffine(b)
	return point.Mult(sk).ToAffine().Compress(), nil
}

// ---------------------------------------------------------------------------
// Buyer side
// ---------------------------------------------------------------------------

// TicketSecret is everything a buyer must keep private for one ticket until
// it is spent.
type TicketSecret struct {
	// PrivateKey is the ticket's Ed25519 private key.
	PrivateKey ed25519.PrivateKey
	// BlindingFactor is the secret scalar r.
	BlindingFactor []byte
	// Blinded is B = r * M, the value sent to the chain.
	Blinded []byte
}

// TicketKey returns the ticket's public identity.
func (t *TicketSecret) TicketKey() []byte {
	return []byte(t.PrivateKey.Public().(ed25519.PublicKey))
}

// NewTicket creates a fresh ticket for one chain and issuer key, and blinds
// it. rand must be a cryptographically secure source.
func NewTicket(rand io.Reader, chainID string, keyID uint64) (*TicketSecret, error) {
	pub, priv, err := ed25519.GenerateKey(rand)
	if err != nil {
		return nil, err
	}

	ikm := make([]byte, 32)
	if _, err := io.ReadFull(rand, ikm); err != nil {
		return nil, err
	}
	// KeyGen maps the random input to a uniform non-zero scalar.
	r := blst.KeyGen(ikm, []byte("SMARTNODES-TICKET-BLIND-V1"))
	if r == nil {
		return nil, ErrInvalidSecret
	}

	m := blst.HashToG1(ticketMessage(chainID, keyID, pub), hashDST)
	blinded := m.Mult(r).ToAffine().Compress()

	return &TicketSecret{
		PrivateKey:     priv,
		BlindingFactor: r.Serialize(),
		Blinded:        blinded,
	}, nil
}

// Unblind removes the blinding factor from the issuer's blind signature and
// checks the result against the issuer public key: S = r^-1 * S'.
func Unblind(t *TicketSecret, publicKey []byte, chainID string, keyID uint64, blindSig []byte) ([]byte, error) {
	r, err := decodeScalar(t.BlindingFactor)
	if err != nil {
		return nil, err
	}
	s, err := decodeG1(blindSig)
	if err != nil {
		return nil, err
	}
	p, err := decodePublicKey(publicKey)
	if err != nil {
		return nil, err
	}

	var point blst.P1
	point.FromAffine(s)
	sig := point.Mult(r.Inverse()).ToAffine()

	m := blst.HashToG1(ticketMessage(chainID, keyID, t.TicketKey()), hashDST).ToAffine()
	if !pairingEqual(sig, m, p) {
		return nil, ErrBadIssuerSig
	}
	return sig.Compress(), nil
}

// Spend builds the ticket a holder hands to a node: it names the node that
// may redeem it and is signed with the ticket's private key.
func Spend(t *TicketSecret, issuerSig []byte, chainID string, keyID, nodeID uint64) ([]byte, error) {
	if len(issuerSig) != G1Size {
		return nil, fmt.Errorf("%w: issuer signature length %d", ErrInvalidTicket, len(issuerSig))
	}
	key := t.TicketKey()
	spendSig := ed25519.Sign(t.PrivateKey, SpendMessage(chainID, keyID, nodeID, key))

	ticket := make([]byte, 0, TicketSize)
	ticket = append(ticket, key...)
	ticket = append(ticket, issuerSig...)
	ticket = append(ticket, spendSig...)
	return ticket, nil
}
