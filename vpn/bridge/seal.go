package bridge

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// A bridge line handed out on the chain is sealed to a one-time key the
// device made for this one request, so only that device can read it. The
// chain, and anyone reading it, sees only the sealed bytes.
//
// Sealing is X25519 key agreement, HKDF-SHA256 and AES-256-GCM:
//
//	sealed = ephemeral public key (32) || nonce (12) || ciphertext
const sealInfo = "SMARTNODES-BRIDGE-ANSWER-V1"

// MaxSealed bounds a sealed answer; a line is under 300 bytes.
const MaxSealed = 1024

// NewRequestKey makes the device's one-time key for a request.
func NewRequestKey() (*ecdh.PrivateKey, error) { return ecdh.X25519().GenerateKey(rand.Reader) }

func sealKey(shared, ephPub, toPub []byte) ([]byte, error) {
	salt := append(append([]byte{}, ephPub...), toPub...)
	return hkdf.Key(sha256.New, shared, salt, sealInfo, 32)
}

// Seal encrypts a line to the device's request key.
func Seal(to []byte, line string) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(to)
	if err != nil {
		return nil, errors.New("bridge: bad request key")
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return nil, err
	}
	key, err := sealKey(shared, eph.PublicKey().Bytes(), to)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(eph.PublicKey().Bytes(), nonce...)
	out = aead.Seal(out, nonce, []byte(line), nil)
	if len(out) > MaxSealed {
		return nil, errors.New("bridge: line too long")
	}
	return out, nil
}

// Open decrypts an answer with the device's request key.
func Open(key *ecdh.PrivateKey, sealed []byte) (string, error) {
	if len(sealed) < 32+12+16 || len(sealed) > MaxSealed {
		return "", errors.New("bridge: bad sealed answer")
	}
	eph, err := ecdh.X25519().NewPublicKey(sealed[:32])
	if err != nil {
		return "", errors.New("bridge: bad sealed answer")
	}
	shared, err := key.ECDH(eph)
	if err != nil {
		return "", err
	}
	k, err := sealKey(shared, sealed[:32], key.PublicKey().Bytes())
	if err != nil {
		return "", err
	}
	block, _ := aes.NewCipher(k)
	aead, _ := cipher.NewGCM(block)
	plain, err := aead.Open(nil, sealed[32:44], sealed[44:], nil)
	if err != nil {
		return "", errors.New("bridge: the answer was not sealed to this key")
	}
	return string(plain), nil
}

// B64 and UnB64 are the text form sealed answers and keys travel in.
func B64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func UnB64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
