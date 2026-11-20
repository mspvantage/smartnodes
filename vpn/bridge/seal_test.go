package bridge

import (
	"bytes"
	"testing"
)

func TestSealOpen(t *testing.T) {
	key, err := NewRequestKey()
	if err != nil {
		t.Fatal(err)
	}
	l := line(t).String()
	sealed, err := Seal(key.PublicKey().Bytes(), l)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("203.0.113.9")) || bytes.Contains(sealed, []byte("smartnodes")) {
		t.Fatal("the sealed answer shows the line")
	}
	got, err := Open(key, sealed)
	if err != nil || got != l {
		t.Fatalf("%q %v", got, err)
	}
	// Only the request's own key opens it.
	other, _ := NewRequestKey()
	if _, err := Open(other, sealed); err == nil {
		t.Fatal("opened with another key")
	}
	// Any change is noticed.
	sealed[len(sealed)-1] ^= 1
	if _, err := Open(key, sealed); err == nil {
		t.Fatal("a changed answer opened")
	}
	// Two seals of the same line differ.
	a, _ := Seal(key.PublicKey().Bytes(), l)
	b, _ := Seal(key.PublicKey().Bytes(), l)
	if bytes.Equal(a, b) {
		t.Fatal("sealing is not randomised")
	}
	if _, err := Seal([]byte{1, 2}, l); err == nil {
		t.Fatal("bad key accepted")
	}
}
