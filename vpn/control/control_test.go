package control

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"testing"
)

const testChain = "smartnodes-test"

func mustKey(t *testing.T) []byte {
	t.Helper()
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return k.Private
}

func helloFor(t *testing.T) Hello {
	t.Helper()
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return Hello{KeyID: 3, Tickets: []string{"dGlja2V0"}, WGPubkey: base64.StdEncoding.EncodeToString(k.Public)}
}

// exchange runs one client and one node over an in-memory connection.
func exchange(t *testing.T, nodePriv []byte, dialKey []byte, dialNode, serveNode uint64, dialChain string, hello Hello, h Handler) (Reply, error, error) {
	t.Helper()
	key, err := KeyFromPrivate(nodePriv)
	if err != nil {
		t.Fatal(err)
	}
	if dialKey == nil {
		dialKey = key.Public
	}
	a, b := net.Pipe()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(b, testChain, serveNode, key, h)
		b.Close()
	}()
	reply, err := Dial(a, dialChain, dialNode, dialKey, hello)
	a.Close()
	return reply, err, <-serveErr
}

func TestExchange(t *testing.T) {
	priv := mustKey(t)
	want := helloFor(t)
	var got Hello
	reply, err, serr := exchange(t, priv, nil, 7, 7, testChain, want, func(h Hello) Reply {
		got = h
		return Reply{OK: true, TunnelIP: "10.77.0.2", Expires: 99}
	})
	if err != nil || serr != nil {
		t.Fatalf("dial %v, serve %v", err, serr)
	}
	if !reply.OK || reply.TunnelIP != "10.77.0.2" || reply.Expires != 99 {
		t.Fatalf("reply %+v", reply)
	}
	if got.KeyID != 3 || got.WGPubkey != want.WGPubkey || len(got.Tickets) != 1 || got.Version != Version {
		t.Fatalf("hello %+v", got)
	}
}

// A node that does not hold the registered key learns nothing and cannot
// answer.
func TestImpostorIsDetected(t *testing.T) {
	registered := mustKey(t)
	regKey, _ := KeyFromPrivate(registered)
	impostor := mustKey(t)
	called := false
	_, err, serr := exchange(t, impostor, regKey.Public, 7, 7, testChain, helloFor(t), func(Hello) Reply {
		called = true
		return Reply{OK: true}
	})
	if err == nil {
		t.Fatal("client accepted an impostor")
	}
	if serr == nil {
		t.Fatal("impostor decrypted the hello")
	}
	if called {
		t.Fatal("impostor saw the hello")
	}
}

// A hello made for one node or chain does not work at another.
func TestBoundToNodeAndChain(t *testing.T) {
	priv := mustKey(t)
	for name, c := range map[string]struct {
		dialNode uint64
		chain    string
	}{
		"other node":  {8, testChain},
		"other chain": {7, "other-chain"},
	} {
		called := false
		_, err, serr := exchange(t, priv, nil, c.dialNode, 7, c.chain, helloFor(t), func(Hello) Reply {
			called = true
			return Reply{OK: true}
		})
		if err == nil || serr == nil || called {
			t.Fatalf("%s: dial %v serve %v called %v", name, err, serr, called)
		}
	}
}

func TestBadHellos(t *testing.T) {
	priv := mustKey(t)
	good := helloFor(t)
	many := good
	many.Tickets = make([]string, MaxTickets+1)
	for i := range many.Tickets {
		many.Tickets[i] = "x"
	}
	cases := map[string]Hello{
		"no tickets":     {KeyID: 1, WGPubkey: good.WGPubkey},
		"too many":       many,
		"bad wg key":     {KeyID: 1, Tickets: good.Tickets, WGPubkey: "AAAA"},
		"wg key not b64": {KeyID: 1, Tickets: good.Tickets, WGPubkey: "!!"},
	}
	for name, h := range cases {
		called := false
		reply, err, serr := exchange(t, priv, nil, 7, 7, testChain, h, func(Hello) Reply {
			called = true
			return Reply{OK: true}
		})
		if err != nil || serr != nil {
			t.Fatalf("%s: dial %v serve %v", name, err, serr)
		}
		if called || reply.OK || reply.Error == "" {
			t.Fatalf("%s: handler called %v, reply %+v", name, called, reply)
		}
	}
}

func TestFrames(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, make([]byte, MaxFrame+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized write: %v", err)
	}
	// A length prefix over the limit is refused before reading the body.
	buf.Reset()
	buf.Write([]byte{0xFF, 0xFF})
	if _, err := readFrame(&buf); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized read: %v", err)
	}
	buf.Reset()
	if err := writeFrame(&buf, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	got, err := readFrame(&buf)
	if err != nil || string(got) != "hi" {
		t.Fatalf("round trip %q %v", got, err)
	}
	// A short body is an error, not a hang or a partial message.
	if _, err := readFrame(bytes.NewReader([]byte{0, 5, 'a'})); err == nil {
		t.Fatal("short frame accepted")
	}
}

// Garbage instead of a handshake is refused without calling the handler.
func TestGarbageHandshake(t *testing.T) {
	key, _ := KeyFromPrivate(mustKey(t))
	a, b := net.Pipe()
	done := make(chan error, 1)
	called := false
	go func() {
		done <- Serve(b, testChain, 1, key, func(Hello) Reply { called = true; return Reply{} })
		b.Close()
	}()
	_ = writeFrame(a, bytes.Repeat([]byte{1}, 200))
	if err := <-done; err == nil || called {
		t.Fatalf("garbage accepted: %v %v", err, called)
	}
	a.Close()
}

func TestKeys(t *testing.T) {
	// The public key of a WireGuard private key, as "wg pubkey" prints it.
	// This pair is RFC 7748's Alice test vector.
	priv, _ := base64.StdEncoding.DecodeString("dwdtCnMYpX08FsFyUbJmRd9ML4frwJkqsXf7pR25LCo=")
	k, err := KeyFromPrivate(priv)
	if err != nil {
		t.Fatal(err)
	}
	if got := base64.StdEncoding.EncodeToString(k.Public); got != "hSDwCYkwp1R0i33ctD73Wg2/Og0mOBr066SpjqqbTmo=" {
		t.Fatalf("public key %s", got)
	}
	if _, err := KeyFromPrivate(priv[:31]); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := KeyFromBase64("not base64!"); err == nil {
		t.Fatal("bad base64 accepted")
	}
	if _, err := DecodePublicKey(strings.Repeat("A", 43) + "="); err != nil {
		t.Fatal(err)
	}
}
