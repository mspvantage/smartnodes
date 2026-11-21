// Package faucet hands out test SNODE on a test network, so testers can buy
// tickets, bond nodes and pay fees without anyone sending them coins.
//
// Limits: one payment per address and one per visitor in each period, and
// a cap on payments per day. Visitors' addresses are kept only as salted
// hashes, in memory, for the length of the period; nothing is logged or
// written to disk.
package faucet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"
)

// Faucet is an HTTP handler that pays test SNODE.
type Faucet struct {
	// Send pays the fixed amount to an address and waits until it is in a
	// block.
	Send func(ctx context.Context, address string) error
	// Prefix is the chain's address prefix, such as "smart".
	Prefix string
	// Amount is shown to visitors, such as "100 SNODE".
	Amount string
	// Period is how long an address, or a visitor, waits between payments.
	Period time.Duration
	// DailyCap is the most payments in one day, counted from midnight UTC.
	DailyCap int
	// TrustProxy reads the visitor's address from the header the proxy in
	// front sets (CF-Connecting-IP, then the last X-Forwarded-For entry).
	// Leave false unless a proxy really is in front, or anyone can fake it.
	TrustProxy bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	once    sync.Once
	mu      sync.Mutex
	salt    []byte
	byAddr  map[string]time.Time
	byWho   map[string]time.Time
	day     string
	today   int
	sending sync.Mutex
}

// Errors visitors may see.
var (
	ErrBadAddress = errors.New("that is not a valid address on this chain")
	ErrTooSoon    = errors.New("this address, or you, already had test SNODE recently; try again later")
	ErrDailyCap   = errors.New("the faucet has given out all it may today; try again tomorrow")
)

func (f *Faucet) init() {
	f.once.Do(func() {
		f.salt = make([]byte, 32)
		_, _ = rand.Read(f.salt)
		f.byAddr = map[string]time.Time{}
		f.byWho = map[string]time.Time{}
		if f.Now == nil {
			f.Now = time.Now
		}
	})
}

// CheckAddress accepts a bech32 account address with the chain's prefix.
func CheckAddress(prefix, address string) error {
	hrp, data, err := bech32.DecodeAndConvert(strings.TrimSpace(address))
	if err != nil || hrp != prefix || (len(data) != 20 && len(data) != 32) {
		return ErrBadAddress
	}
	return nil
}

// visitor is a salted hash of the visitor's network address.
func (f *Faucet) visitor(r *http.Request) string {
	ip := ""
	if f.TrustProxy {
		if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
			ip = v
		} else if v := r.Header.Get("X-Forwarded-For"); v != "" {
			parts := strings.Split(v, ",")
			ip = strings.TrimSpace(parts[len(parts)-1])
		}
	}
	if ip == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip = host
	}
	sum := sha256.Sum256(append(append([]byte{}, f.salt...), ip...))
	return hex.EncodeToString(sum[:])
}

// reserve takes a place for one payment, or says why not. The place is
// given back with release if the payment fails.
func (f *Faucet) reserve(addr, who string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.Now()
	for k, t := range f.byAddr {
		if now.Sub(t) >= f.Period {
			delete(f.byAddr, k)
		}
	}
	for k, t := range f.byWho {
		if now.Sub(t) >= f.Period {
			delete(f.byWho, k)
		}
	}
	if day := now.UTC().Format("2006-01-02"); day != f.day {
		f.day, f.today = day, 0
	}
	if _, ok := f.byAddr[addr]; ok {
		return ErrTooSoon
	}
	if _, ok := f.byWho[who]; ok {
		return ErrTooSoon
	}
	if f.DailyCap > 0 && f.today >= f.DailyCap {
		return ErrDailyCap
	}
	f.byAddr[addr], f.byWho[who] = now, now
	f.today++
	return nil
}

func (f *Faucet) release(addr, who string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byAddr, addr)
	delete(f.byWho, who)
	if f.today > 0 {
		f.today--
	}
}

// Request pays an address, within the limits.
func (f *Faucet) Request(ctx context.Context, r *http.Request, address string) error {
	f.init()
	address = strings.TrimSpace(address)
	if err := CheckAddress(f.Prefix, address); err != nil {
		return err
	}
	who := f.visitor(r)
	if err := f.reserve(address, who); err != nil {
		return err
	}
	// One payment at a time: they come from one account.
	f.sending.Lock()
	err := f.Send(ctx, address)
	f.sending.Unlock()
	if err != nil {
		f.release(address, who)
		// For the operator: why it failed, with the address taken out.
		log.Printf("a payment failed: %s", strings.ReplaceAll(err.Error(), address, "<address>"))
		return errors.New("the payment did not go through; please try again in a minute")
	}
	return nil
}

// ServeHTTP serves a small page with a form at /, and a JSON interface at
// /api for apps: POST {"address": "..."} answers {"ok": true} or
// {"ok": false, "error": "..."}.
func (f *Faucet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.init()
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	switch {
	case r.URL.Path == "/api" && r.Method == http.MethodPost:
		var in struct {
			Address string `json:"address"`
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "send JSON: {\"address\": \"...\"}"})
			return
		}
		if err := f.Request(ctx, r, in.Address); err != nil {
			w.WriteHeader(statusFor(err))
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "amount": f.Amount})
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		f.page(w, http.StatusOK, "")
	case r.URL.Path == "/" && r.Method == http.MethodPost:
		if err := f.Request(ctx, r, r.PostFormValue("address")); err != nil {
			f.page(w, statusFor(err), err.Error())
			return
		}
		f.page(w, http.StatusOK, "Sent "+f.Amount+". It is in your account now.")
	default:
		http.NotFound(w, r)
	}
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, ErrBadAddress):
		return http.StatusBadRequest
	case errors.Is(err, ErrTooSoon), errors.Is(err, ErrDailyCap):
		return http.StatusTooManyRequests
	}
	return http.StatusServiceUnavailable
}

func (f *Faucet) page(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	msg := ""
	if message != "" {
		msg = `<p class="msg">` + html.EscapeString(message) + `</p>`
	}
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>SmartNodes test faucet</title><style>
body{font-family:system-ui,sans-serif;background:#0b1220;color:#e6edf7;max-width:36rem;margin:4rem auto;padding:0 1rem;line-height:1.6}
input{width:100%%;padding:.6rem;border-radius:.4rem;border:1px solid #1f2a40;background:#070c16;color:#e6edf7;font-size:1rem}
button{margin-top:.8rem;padding:.6rem 1.2rem;border:0;border-radius:.4rem;background:#2fb3a4;color:#06201d;font-weight:700;font-size:1rem}
.msg{background:#111a2c;border:1px solid #1f2a40;padding:.8rem;border-radius:.4rem}.note{color:#9aa7bd;font-size:.9rem}a{color:#5fd0c3}
</style></head><body><h1>SmartNodes test faucet</h1>
<p>Free <b>test</b> SNODE for the SmartNodes test network: %s per request. It has no value, and the test network may be reset at any time.</p>%s
<form method="post" action="/"><label for="a">Your address (starts with %s1)</label><input id="a" name="address" autocomplete="off" spellcheck="false" required>
<button type="submit">Send me test SNODE</button></form>
<p class="note">One request per address, and per visitor, every %s. Your network address is kept only as a salted hash, in memory, for that long. Nothing is logged. <a href="https://smartnodes.tech">smartnodes.tech</a></p>
</body></html>`, html.EscapeString(f.Amount), msg, html.EscapeString(f.Prefix), html.EscapeString(humanPeriod(f.Period)))
}

func humanPeriod(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		h := int(d / time.Hour)
		if h == 1 {
			return "hour"
		}
		return fmt.Sprintf("%d hours", h)
	}
	return d.String()
}
