// Command smartfaucet gives out test SNODE on a test network.
//
// Run it behind an HTTPS proxy (Caddy, nginx, or Cloudflare), listening on
// localhost only, and set -trust-proxy so visitors are told apart by the
// address the proxy reports.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/mspvantage/smartnodes/vpn/chain"
	"github.com/mspvantage/smartnodes/vpn/faucet"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "address to serve on")
	amount := flag.String("amount", "100000000usnode", "what each request gets, in usnode")
	period := flag.Duration("period", 24*time.Hour, "how long an address, or a visitor, waits between requests")
	dailyCap := flag.Int("daily-cap", 500, "most requests paid in one day (0: no cap)")
	trustProxy := flag.Bool("trust-proxy", false, "read visitors' addresses from the proxy's headers (only behind a proxy)")
	prefix := flag.String("prefix", "smart", "the chain's address prefix")
	rpc := flag.String("rpc", "tcp://localhost:26657", "chain node to talk to")
	chainID := flag.String("chain-id", "smartnodes", "chain id")
	binary := flag.String("smartnodesd", "smartnodesd", "path to the smartnodesd program")
	from := flag.String("from", "", "key that pays out")
	backend := flag.String("keyring-backend", "test", "keyring backend of that key")
	home := flag.String("home", "", "smartnodesd home folder holding that key")
	gasPrices := flag.String("gas-prices", "0.025usnode", "gas price")
	flag.Parse()

	if err := run(*listen, *amount, *period, *dailyCap, *trustProxy, *prefix, &chain.CLI{
		Run: chain.Exec(*binary), RPC: *rpc, ChainID: *chainID, From: *from,
		KeyringBackend: *backend, Home: *home, GasPrices: *gasPrices,
		PollInterval: time.Second, PollTimeout: time.Minute,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "smartfaucet:", err)
		os.Exit(1)
	}
}

var coin = regexp.MustCompile(`^([0-9]+)usnode$`)

func run(listen, amount string, period time.Duration, dailyCap int, trustProxy bool, prefix string, cli *chain.CLI) error {
	if cli.From == "" {
		return errors.New("-from is required")
	}
	m := coin.FindStringSubmatch(amount)
	if m == nil {
		return errors.New("-amount must be a number of usnode, such as 100000000usnode")
	}
	shown := amount
	if len(m[1]) > 6 && m[1][len(m[1])-6:] == "000000" {
		shown = m[1][:len(m[1])-6] + " test SNODE"
	}
	f := &faucet.Faucet{
		Send:   func(ctx context.Context, to string) error { return cli.BankSend(ctx, to, amount) },
		Prefix: prefix, Amount: shown, Period: period, DailyCap: dailyCap, TrustProxy: trustProxy,
	}
	srv := &http.Server{Addr: listen, Handler: counted(f), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 3 * time.Minute}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("faucet on %s: %s per request, one per address and visitor every %s, at most %d a day", listen, shown, period, dailyCap)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// counted logs only how many payments went out, once an hour, never who
// asked.
func counted(next http.Handler) http.Handler {
	var mu sync.Mutex
	var paid, refused int
	last := time.Now()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &status{ResponseWriter: w, code: 200}
		next.ServeHTTP(rec, r)
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			if rec.code == 200 {
				paid++
			} else {
				refused++
			}
		}
		if time.Since(last) > time.Hour {
			log.Printf("requests paid %d, refused %d", paid, refused)
			last = time.Now()
		}
	})
}

type status struct {
	http.ResponseWriter
	code int
}

func (s *status) WriteHeader(code int) { s.code = code; s.ResponseWriter.WriteHeader(code) }
