package faucet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"
)

func addr(t *testing.T, b byte) string {
	t.Helper()
	s, err := bech32.ConvertAndEncode("smart", []byte(strings.Repeat(string(rune(b)), 20)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type world struct {
	f    *Faucet
	sent []string
	fail bool
	now  time.Time
}

func newWorld() *world {
	w := &world{now: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)}
	w.f = &Faucet{Prefix: "smart", Amount: "100 SNODE", Period: 24 * time.Hour, DailyCap: 3, Now: func() time.Time { return w.now }}
	w.f.Send = func(_ context.Context, a string) error {
		if w.fail {
			return errors.New("node down")
		}
		w.sent = append(w.sent, a)
		return nil
	}
	return w
}

func (w *world) post(t *testing.T, from, address string) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{"address": {address}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = from + ":5555"
	rec := httptest.NewRecorder()
	w.f.ServeHTTP(rec, r)
	return rec.Code
}

func TestLimits(t *testing.T) {
	w := newWorld()
	a, b, c, d := addr(t, 1), addr(t, 2), addr(t, 3), addr(t, 4)
	if code := w.post(t, "198.51.100.1", a); code != 200 {
		t.Fatalf("first: %d", code)
	}
	// The same address from elsewhere, or the same visitor for another
	// address, waits.
	if code := w.post(t, "198.51.100.2", a); code != 429 {
		t.Fatalf("same address: %d", code)
	}
	if code := w.post(t, "198.51.100.1", b); code != 429 {
		t.Fatalf("same visitor: %d", code)
	}
	// Other visitors, until the daily cap.
	if w.post(t, "198.51.100.2", b) != 200 || w.post(t, "198.51.100.3", c) != 200 {
		t.Fatal("other visitors refused")
	}
	if code := w.post(t, "198.51.100.4", d); code != 429 {
		t.Fatalf("over the cap: %d", code)
	}
	// A new day lifts the cap; a period later, all may come again.
	w.now = w.now.Add(25 * time.Hour)
	if code := w.post(t, "198.51.100.1", a); code != 200 {
		t.Fatalf("next day: %d", code)
	}
	if len(w.sent) != 4 {
		t.Fatalf("sent %d", len(w.sent))
	}
}

func TestBadAddressesAndFailures(t *testing.T) {
	w := newWorld()
	for _, bad := range []string{"", "hello", "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu", addr(t, 1) + "x"} {
		if code := w.post(t, "198.51.100.1", bad); code != 400 {
			t.Fatalf("%q: %d", bad, code)
		}
	}
	// A failed payment gives the place back.
	w.fail = true
	if code := w.post(t, "198.51.100.1", addr(t, 1)); code != 503 {
		t.Fatalf("failed send: %d", code)
	}
	w.fail = false
	if code := w.post(t, "198.51.100.1", addr(t, 1)); code != 200 {
		t.Fatalf("after a failure: %d", code)
	}
}

func TestAPIAndProxy(t *testing.T) {
	w := newWorld()
	w.f.TrustProxy = true
	call := func(header, ip, address string) (int, string) {
		r := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader(`{"address":"`+address+`"}`))
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set(header, ip)
		rec := httptest.NewRecorder()
		w.f.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}
	if code, body := call("CF-Connecting-IP", "203.0.113.9", addr(t, 1)); code != 200 || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("%d %s", code, body)
	}
	// Behind the proxy, visitors are told apart by the header, not by the
	// proxy's own address.
	if code, _ := call("CF-Connecting-IP", "203.0.113.10", addr(t, 2)); code != 200 {
		t.Fatalf("second visitor: %d", code)
	}
	if code, body := call("X-Forwarded-For", "10.0.0.1, 203.0.113.9", addr(t, 3)); code != 429 || !strings.Contains(body, `"ok":false`) {
		t.Fatalf("same visitor via X-Forwarded-For: %d %s", code, body)
	}
}

func TestPageEscapes(t *testing.T) {
	w := newWorld()
	if code := w.post(t, "198.51.100.1", "<script>"); code != 400 {
		t.Fatal(code)
	}
	rec := httptest.NewRecorder()
	w.f.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "test faucet") || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("%d", rec.Code)
	}
}
