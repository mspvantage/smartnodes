package issuer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
)

type fakeChain struct {
	pub       []byte
	height    int64
	purchases []Purchase
	fulfilled map[uint64][]string
	failNext  bool
}

func (f *fakeChain) IssuerPublicKey(context.Context, uint64) ([]byte, error) { return f.pub, nil }
func (f *fakeChain) Height(context.Context) (int64, error)                   { return f.height, nil }
func (f *fakeChain) Purchases(context.Context) ([]Purchase, error)           { return f.purchases, nil }
func (f *fakeChain) FulfilPurchase(_ context.Context, id uint64, sigs []string) error {
	if f.failNext {
		f.failNext = false
		return errors.New("chain refused")
	}
	if f.fulfilled == nil {
		f.fulfilled = map[uint64][]string{}
	}
	f.fulfilled[id] = sigs
	for n := range f.purchases {
		if f.purchases[n].ID == id {
			f.purchases[n].Fulfilled = true
		}
	}
	return nil
}

func keys(t *testing.T) (sk, pk []byte) {
	t.Helper()
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	sk, pk, err := ticketcrypto.GenerateIssuerKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	return sk, pk
}

func blinded(t *testing.T, n int) ([]*ticketcrypto.TicketSecret, []string) {
	t.Helper()
	var secrets []*ticketcrypto.TicketSecret
	var out []string
	for i := 0; i < n; i++ {
		s, err := ticketcrypto.NewTicket(rand.Reader, "smartnodes", 0)
		if err != nil {
			t.Fatal(err)
		}
		secrets = append(secrets, s)
		out = append(out, base64.StdEncoding.EncodeToString(s.Blinded))
	}
	return secrets, out
}

func TestSignsOpenPurchases(t *testing.T) {
	sk, pk := keys(t)
	secrets, bl := blinded(t, 3)
	_, other := blinded(t, 1)
	c := &fakeChain{pub: pk, height: 100, purchases: []Purchase{
		{ID: 1, KeyID: 0, Blinded: bl, Deadline: 150},
		{ID: 2, KeyID: 7, Blinded: other, Deadline: 150},                  // another key
		{ID: 3, KeyID: 0, Blinded: other, Deadline: 150, Fulfilled: true}, // done
		{ID: 4, KeyID: 0, Blinded: other, Deadline: 101},                  // too late
	}}
	is, err := New(context.Background(), c, 0, sk, pk)
	if err != nil {
		t.Fatal(err)
	}
	r, err := is.Once(context.Background())
	if err != nil || r.Signed != 1 || r.Tickets != 3 || r.Skipped != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	// The signatures unblind into tickets the chain would accept.
	for i, s := range c.fulfilled[1] {
		sig, _ := base64.StdEncoding.DecodeString(s)
		if _, err := ticketcrypto.Unblind(secrets[i], pk, "smartnodes", 0, sig); err != nil {
			t.Fatalf("ticket %d: %v", i, err)
		}
	}
	if len(c.fulfilled) != 1 {
		t.Fatalf("signed %d purchases", len(c.fulfilled))
	}
	// A second pass has nothing to do.
	if r, _ := is.Once(context.Background()); r.Signed != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestRetriesThenGivesUp(t *testing.T) {
	sk, pk := keys(t)
	_, bl := blinded(t, 1)
	c := &fakeChain{pub: pk, height: 10, purchases: []Purchase{{ID: 1, Blinded: bl, Deadline: 100}}, failNext: true}
	is, _ := New(context.Background(), c, 0, sk, pk)
	if r, _ := is.Once(context.Background()); r.Failed != 1 {
		t.Fatalf("%+v", r)
	}
	if r, _ := is.Once(context.Background()); r.Signed != 1 {
		t.Fatalf("retry: %+v", r)
	}
	// A purchase that can never be signed is tried MaxTries times only.
	c.purchases = append(c.purchases, Purchase{ID: 2, Blinded: []string{"not base64!"}, Deadline: 100})
	for i := 0; i < MaxTries+2; i++ {
		_, _ = is.Once(context.Background())
	}
	if is.tries[2] != MaxTries {
		t.Fatalf("tried %d times", is.tries[2])
	}
}

func TestWrongKeyRefused(t *testing.T) {
	sk, pk := keys(t)
	_, otherPK := keys(t)
	if _, err := New(context.Background(), &fakeChain{pub: otherPK}, 0, sk, pk); err == nil {
		t.Fatal("a key file for another key was accepted")
	}
	otherSK, _ := keys(t)
	if _, err := New(context.Background(), &fakeChain{pub: pk}, 0, otherSK, pk); err == nil {
		t.Fatal("a secret key that does not match was accepted")
	}
}
