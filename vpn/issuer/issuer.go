// Package issuer signs ticket purchases automatically, for a test network.
//
// A buyer posts blinded tickets and pays; the issuer signs each blinded
// value without seeing what it hides, and posts the signatures. The chain
// checks every signature, so a mistake here can only fail, never mint.
//
// This is a stage A tool: one issuer, one key. Before real value, signing
// is split between several independent issuers (threshold issuers), so that
// no single key can create tickets.
package issuer

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
)

// Purchase is a ticket purchase as the chain holds it.
type Purchase struct {
	ID        uint64
	KeyID     uint64
	Blinded   []string
	Deadline  int64
	Fulfilled bool
}

// Chain is what the issuer needs from the chain.
type Chain interface {
	IssuerPublicKey(ctx context.Context, keyID uint64) ([]byte, error)
	Height(ctx context.Context) (int64, error)
	Purchases(ctx context.Context) ([]Purchase, error)
	FulfilPurchase(ctx context.Context, id uint64, signatures []string) error
}

// MaxTries is how often a purchase is tried before the issuer gives up on
// it. A purchase it gives up on is refunded by the chain at its deadline.
const MaxTries = 3

// Issuer signs purchases made under one issuer key.
type Issuer struct {
	chain  Chain
	keyID  uint64
	secret []byte
	tries  map[uint64]int
}

// New checks that the secret key belongs to the issuer key on the chain, so
// a wrong key file is caught before anything is signed.
func New(ctx context.Context, c Chain, keyID uint64, secret, public []byte) (*Issuer, error) {
	onChain, err := c.IssuerPublicKey(ctx, keyID)
	if err != nil {
		return nil, fmt.Errorf("issuer: reading key %d from the chain: %w", keyID, err)
	}
	if !bytes.Equal(onChain, public) {
		return nil, fmt.Errorf("issuer: the key file is not issuer key %d on the chain", keyID)
	}
	// Sign a test value with the secret key and check it against the public
	// key, so a file whose two halves do not match is caught too.
	probe, err := testBlinded()
	if err != nil {
		return nil, err
	}
	sig, err := ticketcrypto.BlindSign(secret, probe)
	if err != nil {
		return nil, fmt.Errorf("issuer: unusable secret key: %w", err)
	}
	if err := ticketcrypto.VerifyBlindSignature(public, probe, sig); err != nil {
		return nil, errors.New("issuer: the secret key does not match the public key")
	}
	return &Issuer{chain: c, keyID: keyID, secret: secret, tries: map[uint64]int{}}, nil
}

// testBlinded makes a valid blinded value to sign as a check.
func testBlinded() ([]byte, error) {
	t, err := ticketcrypto.NewTicket(randReader, "issuer-self-check", 0)
	if err != nil {
		return nil, err
	}
	return t.Blinded, nil
}

// Result counts what one pass did.
type Result struct {
	Signed, Tickets, Failed, Skipped int
}

// Once signs every open purchase under the issuer's key that can still be
// fulfilled in time. Each purchase is tried at most MaxTries times.
func (i *Issuer) Once(ctx context.Context) (Result, error) {
	var r Result
	height, err := i.chain.Height(ctx)
	if err != nil {
		return r, err
	}
	purchases, err := i.chain.Purchases(ctx)
	if err != nil {
		return r, err
	}
	open := map[uint64]bool{}
	for _, p := range purchases {
		if p.Fulfilled || p.KeyID != i.keyID {
			continue
		}
		open[p.ID] = true
		// Too close to its deadline: it would be refunded before the
		// signatures land.
		if p.Deadline <= height+1 || i.tries[p.ID] >= MaxTries {
			r.Skipped++
			continue
		}
		i.tries[p.ID]++
		sigs, err := i.sign(p.Blinded)
		if err == nil {
			err = i.chain.FulfilPurchase(ctx, p.ID, sigs)
		}
		if err != nil {
			r.Failed++
			continue
		}
		r.Signed++
		r.Tickets += len(sigs)
	}
	// Forget purchases that are no longer open.
	for id := range i.tries {
		if !open[id] {
			delete(i.tries, id)
		}
	}
	return r, nil
}

func (i *Issuer) sign(blinded []string) ([]string, error) {
	if len(blinded) == 0 {
		return nil, errors.New("issuer: empty purchase")
	}
	out := make([]string, 0, len(blinded))
	for n, text := range blinded {
		b, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, fmt.Errorf("issuer: blinded value %d is not base64", n)
		}
		sig, err := ticketcrypto.BlindSign(i.secret, b)
		if err != nil {
			return nil, fmt.Errorf("issuer: blinded value %d: %w", n, err)
		}
		out = append(out, base64.StdEncoding.EncodeToString(sig))
	}
	return out, nil
}
