// Package wallet spends tickets from a buyer's wallet file, the format
// ticketctl writes, for a client that pays nodes by itself.
//
// A ticket is marked used and the file saved before the spend leaves this
// package, so a crash can never hand the same ticket out twice. The file
// records only that a ticket was used, never which node it paid.
package wallet

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
)

// ErrEmpty means every ticket has been used.
var ErrEmpty = errors.New("wallet: no unused tickets left")

type ticket struct {
	PrivateKey     string `json:"private_key"`
	BlindingFactor string `json:"blinding_factor"`
	Blinded        string `json:"blinded"`
	Signature      string `json:"signature,omitempty"`
	Used           bool   `json:"used,omitempty"`
}

type file struct {
	ChainID string   `json:"chain_id"`
	KeyID   uint64   `json:"key_id"`
	Tickets []ticket `json:"tickets"`
}

// Wallet is an open wallet file.
type Wallet struct {
	path string
	mu   sync.Mutex
	f    file
}

// Open reads a wallet file. It must be readable by its owner only.
func Open(path string) (*Wallet, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("wallet: %s is readable by others; it must be mode 0600", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	w := &Wallet{path: path}
	if err := json.Unmarshal(raw, &w.f); err != nil {
		return nil, fmt.Errorf("wallet: unreadable %s: %w", path, err)
	}
	return w, nil
}

// ChainID and KeyID say what the tickets are for.
func (w *Wallet) ChainID() string { return w.f.ChainID }
func (w *Wallet) KeyID() uint64   { return w.f.KeyID }

// Left counts unused, signed tickets.
func (w *Wallet) Left() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, t := range w.f.Tickets {
		if !t.Used && t.Signature != "" {
			n++
		}
	}
	return n
}

// Spend makes out the next unused ticket to a node.
func (w *Wallet) Spend(nodeID uint64) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.f.Tickets {
		t := &w.f.Tickets[i]
		if t.Used || t.Signature == "" {
			continue
		}
		spent, err := spend(*t, w.f.ChainID, w.f.KeyID, nodeID)
		// Used either way: a ticket that cannot be spent is skipped next time.
		t.Used = true
		if serr := w.save(); serr != nil {
			t.Used = false
			return "", serr
		}
		if err != nil {
			return "", fmt.Errorf("wallet: ticket %d: %w", i, err)
		}
		return spent, nil
	}
	return "", ErrEmpty
}

func spend(t ticket, chainID string, keyID, nodeID uint64) (string, error) {
	dec := func(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
	priv, err1 := dec(t.PrivateKey)
	factor, err2 := dec(t.BlindingFactor)
	blinded, err3 := dec(t.Blinded)
	sig, err4 := dec(t.Signature)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return "", errors.New("bad base64")
	}
	out, err := ticketcrypto.Spend(&ticketcrypto.TicketSecret{PrivateKey: priv, BlindingFactor: factor, Blinded: blinded}, sig, chainID, keyID, nodeID)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

// save writes the file in one step, owner-readable only.
func (w *Wallet) save() error {
	raw, err := json.MarshalIndent(w.f, "", "  ")
	if err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}
