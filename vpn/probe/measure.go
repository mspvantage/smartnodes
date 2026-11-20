package probe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// Known is content whose correct form is known in advance: an ordinary
// public file and its SHA-256, learned by fetching it outside the tunnel.
type Known struct {
	URL    string
	SHA256 string
}

// Fixed are names whose answers never change, so a lie can be told apart
// from a different but honest answer. Ordinary names give different,
// equally correct answers in different places.
var Fixed = map[string][]string{
	"dns.google":      {"8.8.8.8", "8.8.4.4"},
	"one.one.one.one": {"1.1.1.1", "1.0.0.1"},
}

// Fetch downloads a URL and returns its SHA-256 and size.
func Fetch(ctx context.Context, client *http.Client, url string, limit int64) (sum string, n int64, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	h := sha256.New()
	n, err = io.Copy(h, io.LimitReader(resp.Body, limit))
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// CheckIntegrity fetches each known file and compares it. A file that
// cannot be fetched at all is not counted as altered.
func CheckIntegrity(ctx context.Context, client *http.Client, known []Known) (ok bool, fetched int) {
	ok = true
	for _, k := range known {
		sum, _, err := Fetch(ctx, client, k.URL, 16<<20)
		if err != nil {
			continue
		}
		fetched++
		if sum != k.SHA256 {
			ok = false
		}
	}
	return ok, fetched
}

// Resolver looks up IPv4 addresses. Tests replace it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// CheckDNS looks up names with fixed answers. Every answer must be one of
// the known ones. A name that cannot be looked up at all is not a lie.
func CheckDNS(ctx context.Context, r Resolver, fixed map[string][]string) (ok bool, answered int) {
	ok = true
	for name, want := range fixed {
		got, err := r.LookupNetIP(ctx, "ip4", name)
		if err != nil || len(got) == 0 {
			continue
		}
		answered++
		for _, a := range got {
			if !contains(want, a.Unmap().String()) {
				ok = false
			}
		}
	}
	return ok, answered
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Speed downloads from url for at most d and returns megabits per second.
func Speed(ctx context.Context, client *http.Client, url string, d time.Duration) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	n, err := io.Copy(io.Discard, resp.Body)
	secs := time.Since(start).Seconds()
	if n == 0 {
		if err == nil {
			err = errors.New("nothing downloaded")
		}
		return 0, err
	}
	// A download cut off by the time limit still gives a fair speed.
	return float64(n) * 8 / secs / 1e6, nil
}

// Tools are what a measurement needs.
type Tools struct {
	Client   *http.Client
	Resolver Resolver
}

// DefaultTools fetch through the system's routes and name servers, which
// inside a probe's network space are the tunnels and the exit's name server.
func DefaultTools() Tools {
	return Tools{
		Client:   &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: nil, ForceAttemptHTTP2: true}},
		Resolver: &net.Resolver{PreferGo: true},
	}
}

// MeasureThrough measures a working two-hop connection.
func MeasureThrough(ctx context.Context, t Tools, speedURL string, speedTime time.Duration, known []Known, fixed map[string][]string) Measurement {
	m := Measurement{Reached: BothHops}
	mbps, err := Speed(ctx, t.Client, speedURL, speedTime)
	if err == nil {
		m.Mbps = mbps
	}
	intOK, fetched := CheckIntegrity(ctx, t.Client, known)
	dnsOK, answered := CheckDNS(ctx, t.Resolver, fixed)
	m.IntegrityOK, m.DNSOK = intOK, dnsOK
	// Something must have come through for the probe to judge anything.
	m.Checked = err == nil || fetched > 0 || answered > 0
	return m
}
