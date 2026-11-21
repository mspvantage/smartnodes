package chain

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"

	"github.com/mspvantage/smartnodes/vpn/issuer"
)

var _ issuer.Chain = (*CLI)(nil)

// IssuerPublicKey reads an issuer key's public key.
func (c *CLI) IssuerPublicKey(ctx context.Context, keyID uint64) ([]byte, error) {
	k, err := c.IssuerKey(ctx, keyID)
	if err != nil {
		return nil, err
	}
	return k.Pubkey, nil
}

type purchaseJSON struct {
	ID         number   `json:"id"`
	KeyID      number   `json:"key_id"`
	KeyIDC     number   `json:"keyId"`
	Blinded    []string `json:"blinded"`
	Deadline   number   `json:"deadline"`
	Signatures []string `json:"signatures"`
}

// Purchases lists the purchases the chain holds: open ones, and fulfilled
// ones until they are deleted. A test network holds few at a time.
func (c *CLI) Purchases(ctx context.Context) ([]issuer.Purchase, error) {
	var res struct {
		Purchase []purchaseJSON `json:"purchase"`
	}
	// Pagination flags are named --page-limit on this chain's command line.
	if err := c.query(ctx, &res, "tickets", "list-purchase", "--page-limit", "10000"); err != nil {
		return nil, err
	}
	out := make([]issuer.Purchase, 0, len(res.Purchase))
	for _, p := range res.Purchase {
		key := p.KeyID
		if key == 0 {
			key = p.KeyIDC
		}
		out = append(out, issuer.Purchase{ID: uint64(p.ID), KeyID: uint64(key), Blinded: p.Blinded, Deadline: int64(p.Deadline), Fulfilled: len(p.Signatures) > 0})
	}
	return out, nil
}

// FulfilPurchase posts an issuer's blind signatures for a purchase.
func (c *CLI) FulfilPurchase(ctx context.Context, id uint64, signatures []string) error {
	for _, s := range signatures {
		if _, err := base64.StdEncoding.DecodeString(s); err != nil {
			return fmt.Errorf("chain: a signature is not base64")
		}
	}
	args := append([]string{"tickets", "fulfil-purchase", strconv.FormatUint(id, 10)}, signatures...)
	return c.Send(ctx, args...)
}

// BankSend sends coins from the signing key to an address.
func (c *CLI) BankSend(ctx context.Context, to, amount string) error {
	return c.Send(ctx, "bank", "send", c.From, to, amount)
}
