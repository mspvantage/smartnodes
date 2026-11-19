package wallet

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// makeWallet writes a wallet of n signed tickets, as ticketctl would.
func makeWallet(t *testing.T, n int) string {
	t.Helper()
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	sk, pk, err := ticketcrypto.GenerateIssuerKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	f := file{ChainID: "smartnodes", KeyID: 0}
	for i := 0; i < n; i++ {
		s, err := ticketcrypto.NewTicket(rand.Reader, "smartnodes", 0)
		if err != nil {
			t.Fatal(err)
		}
		blindSig, err := ticketcrypto.BlindSign(sk, s.Blinded)
		if err != nil {
			t.Fatal(err)
		}
		sig, err := ticketcrypto.Unblind(s, pk, "smartnodes", 0, blindSig)
		if err != nil {
			t.Fatal(err)
		}
		f.Tickets = append(f.Tickets, ticket{PrivateKey: b64(s.PrivateKey), BlindingFactor: b64(s.BlindingFactor), Blinded: b64(s.Blinded), Signature: b64(sig)})
	}
	// One not yet signed, which must be skipped.
	f.Tickets = append(f.Tickets, ticket{PrivateKey: "AA==", BlindingFactor: "AA==", Blinded: "AA=="})
	raw, _ := json.Marshal(f)
	path := filepath.Join(t.TempDir(), "wallet.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSpendMarksAndPersists(t *testing.T) {
	path := makeWallet(t, 2)
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if w.Left() != 2 || w.ChainID() != "smartnodes" {
		t.Fatalf("left %d", w.Left())
	}
	a, err := w.Spend(4)
	if err != nil {
		t.Fatal(err)
	}
	// Reopening sees the first ticket used, as after a crash.
	w2, err := Open(path)
	if err != nil || w2.Left() != 1 {
		t.Fatalf("after reopen: %v left %d", err, w2.Left())
	}
	b, err := w2.Spend(5)
	if err != nil || a == b {
		t.Fatalf("second spend %v, same=%v", err, a == b)
	}
	if _, err := w2.Spend(5); err != ErrEmpty {
		t.Fatalf("want ErrEmpty, got %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), `"node`) {
		t.Fatal("the wallet records which node a ticket paid")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

func TestOpenRefusesReadableFile(t *testing.T) {
	path := makeWallet(t, 1)
	_ = os.Chmod(path, 0o644)
	if _, err := Open(path); err == nil {
		t.Fatal("opened a world-readable wallet")
	}
}
