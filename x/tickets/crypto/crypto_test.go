package crypto

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

const (
	testChain = "smartnodes-test"
	otherChn  = "another-chain"
)

func mustIssuer(t *testing.T, seedByte byte) (sk, pk []byte) {
	t.Helper()
	seed := bytes.Repeat([]byte{seedByte}, 32)
	sk, pk, err := GenerateIssuerKey(seed)
	if err != nil {
		t.Fatalf("GenerateIssuerKey: %v", err)
	}
	return sk, pk
}

// issue runs the whole buyer/issuer exchange and returns the ticket secret
// and the unblinded issuer signature.
func issue(t *testing.T, sk, pk []byte, chainID string, keyID uint64) (*TicketSecret, []byte) {
	t.Helper()
	secret, err := NewTicket(rand.Reader, chainID, keyID)
	if err != nil {
		t.Fatalf("NewTicket: %v", err)
	}
	if err := ValidateBlinded(secret.Blinded); err != nil {
		t.Fatalf("ValidateBlinded: %v", err)
	}
	blindSig, err := BlindSign(sk, secret.Blinded)
	if err != nil {
		t.Fatalf("BlindSign: %v", err)
	}
	if err := VerifyBlindSignature(pk, secret.Blinded, blindSig); err != nil {
		t.Fatalf("VerifyBlindSignature: %v", err)
	}
	sig, err := Unblind(secret, pk, chainID, keyID, blindSig)
	if err != nil {
		t.Fatalf("Unblind: %v", err)
	}
	return secret, sig
}

func TestSizes(t *testing.T) {
	sk, pk := mustIssuer(t, 1)
	if len(sk) != SecretKeySize || len(pk) != PublicKeySize {
		t.Fatalf("key sizes: sk %d pk %d", len(sk), len(pk))
	}
	secret, sig := issue(t, sk, pk, testChain, 7)
	if len(secret.Blinded) != G1Size || len(sig) != G1Size {
		t.Fatalf("point sizes: blinded %d sig %d", len(secret.Blinded), len(sig))
	}
	ticket, err := Spend(secret, sig, testChain, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticket) != TicketSize || TicketSize != 144 {
		t.Fatalf("ticket size %d, constant %d", len(ticket), TicketSize)
	}
}

func TestRoundTrip(t *testing.T) {
	sk, pk := mustIssuer(t, 1)
	for i := 0; i < 20; i++ {
		secret, sig := issue(t, sk, pk, testChain, 7)
		ticket, err := Spend(secret, sig, testChain, 7, 42)
		if err != nil {
			t.Fatal(err)
		}
		key, err := VerifyTicket(pk, testChain, 7, 42, ticket)
		if err != nil {
			t.Fatalf("VerifyTicket: %v", err)
		}
		if !bytes.Equal(key, secret.TicketKey()) {
			t.Fatal("VerifyTicket returned the wrong ticket key")
		}
	}
}

// TestUnblindedSignatureIsDeterministic checks the core algebra: whatever
// blinding factor is used, the unblinded signature on a given ticket is the
// same value, s*M. That is what makes the issuer unable to recognise it.
func TestBlindedValuesDiffer(t *testing.T) {
	sk, pk := mustIssuer(t, 1)
	a, sigA := issue(t, sk, pk, testChain, 7)
	b, sigB := issue(t, sk, pk, testChain, 7)
	if bytes.Equal(a.Blinded, b.Blinded) {
		t.Fatal("two tickets produced the same blinded value")
	}
	if bytes.Equal(sigA, sigB) {
		t.Fatal("two tickets produced the same signature")
	}
	if bytes.Equal(a.Blinded, sigA) {
		t.Fatal("blinded value equals the signature")
	}
}

func TestVerifyTicketRejects(t *testing.T) {
	sk, pk := mustIssuer(t, 1)
	_, otherPK := mustIssuer(t, 2)
	secret, sig := issue(t, sk, pk, testChain, 7)
	ticket, err := Spend(secret, sig, testChain, 7, 42)
	if err != nil {
		t.Fatal(err)
	}

	flip := func(i int) []byte {
		c := append([]byte(nil), ticket...)
		c[i] ^= 0x01
		return c
	}

	tests := []struct {
		name    string
		pk      []byte
		chain   string
		keyID   uint64
		nodeID  uint64
		ticket  []byte
		wantErr error
	}{
		{"another node tries to redeem", pk, testChain, 7, 43, ticket, ErrBadSpendSig},
		{"replayed on another chain", pk, otherChn, 7, 42, ticket, ErrBadSpendSig},
		{"replayed under another key id", pk, testChain, 8, 42, ticket, ErrBadSpendSig},
		{"ticket key altered", pk, testChain, 7, 42, flip(0), ErrBadSpendSig},
		{"spend signature altered", pk, testChain, 7, 42, flip(TicketSize - 1), ErrBadSpendSig},
		{"signed by a different issuer", otherPK, testChain, 7, 42, ticket, ErrBadIssuerSig},
		{"truncated", pk, testChain, 7, 42, ticket[:TicketSize-1], ErrInvalidTicket},
		{"empty", pk, testChain, 7, 42, nil, ErrInvalidTicket},
		{"bad public key", pk[:PublicKeySize-1], testChain, 7, 42, ticket, ErrInvalidPublicKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyTicket(tc.pk, tc.chain, tc.keyID, tc.nodeID, tc.ticket)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}

	// altering the issuer signature must fail, as an invalid point or a bad signature
	if _, err := VerifyTicket(pk, testChain, 7, 42, flip(TicketKeySize+G1Size-1)); err == nil {
		t.Fatal("altered issuer signature was accepted")
	}
}

// TestSpendCannotBeRedirected is the theft-in-transit case: someone who sees
// a ticket cannot make it payable to a different node, because that needs the
// ticket's private key.
func TestSpendCannotBeRedirected(t *testing.T) {
	sk, pk := mustIssuer(t, 1)
	secret, sig := issue(t, sk, pk, testChain, 7)
	ticket, _ := Spend(secret, sig, testChain, 7, 42)

	// the thief keeps the ticket key and issuer signature, and signs with its own key
	thief, _ := NewTicket(rand.Reader, testChain, 7)
	forged, _ := Spend(thief, sig, testChain, 7, 99)
	copy(forged[:TicketKeySize], ticket[:TicketKeySize])
	if _, err := VerifyTicket(pk, testChain, 7, 99, forged); !errors.Is(err, ErrBadSpendSig) {
		t.Fatalf("redirected ticket: got %v, want %v", err, ErrBadSpendSig)
	}

	// a signature issued for one ticket does not validate another ticket key
	other, _ := NewTicket(rand.Reader, testChain, 7)
	stolen, _ := Spend(other, sig, testChain, 7, 99)
	if _, err := VerifyTicket(pk, testChain, 7, 99, stolen); !errors.Is(err, ErrBadIssuerSig) {
		t.Fatalf("signature reused on another ticket: got %v, want %v", err, ErrBadIssuerSig)
	}
}

func TestVerifyBlindSignatureRejects(t *testing.T) {
	sk, pk := mustIssuer(t, 1)
	otherSK, otherPK := mustIssuer(t, 2)
	a, _ := NewTicket(rand.Reader, testChain, 7)
	b, _ := NewTicket(rand.Reader, testChain, 7)
	sigA, _ := BlindSign(sk, a.Blinded)
	wrongKeySig, _ := BlindSign(otherSK, a.Blinded)

	if err := VerifyBlindSignature(pk, a.Blinded, sigA); err != nil {
		t.Fatalf("valid blind signature rejected: %v", err)
	}
	if err := VerifyBlindSignature(pk, b.Blinded, sigA); !errors.Is(err, ErrBadBlindSig) {
		t.Fatalf("signature for another blinded value: got %v", err)
	}
	if err := VerifyBlindSignature(pk, a.Blinded, wrongKeySig); !errors.Is(err, ErrBadBlindSig) {
		t.Fatalf("signature by another issuer: got %v", err)
	}
	if err := VerifyBlindSignature(otherPK, a.Blinded, sigA); !errors.Is(err, ErrBadBlindSig) {
		t.Fatalf("checked against another public key: got %v", err)
	}
	// the buyer cannot pass off the blinded value itself as its signature
	if err := VerifyBlindSignature(pk, a.Blinded, a.Blinded); !errors.Is(err, ErrBadBlindSig) {
		t.Fatalf("blinded value used as signature: got %v", err)
	}
}

// TestRejectsMalformedPoints covers the inputs an attacker controls: the
// identity point, bytes that are not on the curve, and wrong lengths.
func TestRejectsMalformedPoints(t *testing.T) {
	sk, pk := mustIssuer(t, 1)

	identityG1 := make([]byte, G1Size)
	identityG1[0] = 0xc0 // compressed encoding of the point at infinity
	identityG2 := make([]byte, PublicKeySize)
	identityG2[0] = 0xc0

	badG1 := [][]byte{
		nil,
		{},
		make([]byte, G1Size),               // all zero: compression flag missing
		bytes.Repeat([]byte{0xff}, G1Size), // not a field element
		identityG1,                         // the identity
		make([]byte, G1Size-1),             // short
		make([]byte, G1Size+1),             // long
		append([]byte{0x80}, make([]byte, G1Size-1)...), // x = 0 is not on the curve
	}
	for i, b := range badG1 {
		if err := ValidateBlinded(b); !errors.Is(err, ErrInvalidPoint) {
			t.Errorf("bad G1 #%d accepted or wrong error: %v", i, err)
		}
		if _, err := BlindSign(sk, b); err == nil {
			t.Errorf("BlindSign accepted bad G1 #%d", i)
		}
	}

	badG2 := [][]byte{nil, make([]byte, PublicKeySize), bytes.Repeat([]byte{0xff}, PublicKeySize), identityG2, pk[:PublicKeySize-1]}
	for i, b := range badG2 {
		if err := ValidatePublicKey(b); !errors.Is(err, ErrInvalidPublicKey) {
			t.Errorf("bad G2 #%d accepted or wrong error: %v", i, err)
		}
	}
	if err := ValidatePublicKey(pk); err != nil {
		t.Fatalf("valid public key rejected: %v", err)
	}

	// an identity "signature" must never verify, whatever the blinded value
	a, _ := NewTicket(rand.Reader, testChain, 7)
	if err := VerifyBlindSignature(pk, a.Blinded, identityG1); err == nil {
		t.Fatal("identity accepted as a blind signature")
	}
	if err := VerifyBlindSignature(pk, identityG1, identityG1); err == nil {
		t.Fatal("identity accepted as blinded value and signature")
	}
}

func TestIssuerKeyRules(t *testing.T) {
	if _, _, err := GenerateIssuerKey(make([]byte, 31)); err == nil {
		t.Fatal("short seed accepted")
	}
	sk1, pk1 := mustIssuer(t, 1)
	sk1b, pk1b := mustIssuer(t, 1)
	sk2, pk2 := mustIssuer(t, 2)
	if !bytes.Equal(sk1, sk1b) || !bytes.Equal(pk1, pk1b) {
		t.Fatal("key generation is not deterministic for the same seed")
	}
	if bytes.Equal(sk1, sk2) || bytes.Equal(pk1, pk2) {
		t.Fatal("different seeds gave the same key")
	}
	if _, err := BlindSign(make([]byte, SecretKeySize), pk1[:G1Size]); err == nil {
		t.Fatal("zero secret key accepted")
	}
}

// TestUnblindRejectsWrongInputs checks that the buyer notices a bad signature
// from the issuer before trying to spend the ticket.
func TestUnblindRejectsWrongInputs(t *testing.T) {
	sk, pk := mustIssuer(t, 1)
	otherSK, _ := mustIssuer(t, 2)
	secret, _ := NewTicket(rand.Reader, testChain, 7)
	good, _ := BlindSign(sk, secret.Blinded)
	bad, _ := BlindSign(otherSK, secret.Blinded)

	if _, err := Unblind(secret, pk, testChain, 7, good); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if _, err := Unblind(secret, pk, testChain, 7, bad); !errors.Is(err, ErrBadIssuerSig) {
		t.Fatalf("signature from the wrong issuer: got %v", err)
	}
	if _, err := Unblind(secret, pk, otherChn, 7, good); !errors.Is(err, ErrBadIssuerSig) {
		t.Fatalf("wrong chain id: got %v", err)
	}
	if _, err := Unblind(secret, pk, testChain, 8, good); !errors.Is(err, ErrBadIssuerSig) {
		t.Fatalf("wrong key id: got %v", err)
	}
}

// TestMessagesAreUnambiguous guards the length prefix: moving a byte between
// the chain id and the fields after it must change the message.
func TestMessagesAreUnambiguous(t *testing.T) {
	key := bytes.Repeat([]byte{9}, TicketKeySize)
	if bytes.Equal(ticketMessage("ab", 1, key), ticketMessage("a", 1, append([]byte("b"), key...)[:TicketKeySize])) {
		t.Fatal("ticket messages collide")
	}
	if bytes.Equal(SpendMessage("chain", 1, 2, key), SpendMessage("chain", 2, 1, key)) {
		t.Fatal("spend messages collide when key id and node id swap")
	}
	if bytes.Equal(SpendMessage("chain", 1, 2, key), ticketMessage("chain", 1, key)) {
		t.Fatal("spend message equals ticket message")
	}
}

func BenchmarkVerifyTicket(b *testing.B) {
	seed := bytes.Repeat([]byte{1}, 32)
	sk, pk, _ := GenerateIssuerKey(seed)
	secret, _ := NewTicket(rand.Reader, testChain, 7)
	blindSig, _ := BlindSign(sk, secret.Blinded)
	sig, _ := Unblind(secret, pk, testChain, 7, blindSig)
	ticket, _ := Spend(secret, sig, testChain, 7, 42)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := VerifyTicket(pk, testChain, 7, 42, ticket); err != nil {
			b.Fatal(err)
		}
	}
}
