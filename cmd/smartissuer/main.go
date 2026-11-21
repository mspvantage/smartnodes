// Command smartissuer signs ticket purchases automatically on a test
// network, so testers get their tickets without anyone signing by hand.
//
// It holds one issuer secret key and must never be used for anything of
// value: before real value, signing is split between several independent
// issuers.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mspvantage/smartnodes/vpn/chain"
	"github.com/mspvantage/smartnodes/vpn/issuer"
)

type issuerFile struct {
	SecretKey string `json:"secret_key"`
	PublicKey string `json:"public_key"`
}

func main() {
	keyFile := flag.String("issuer", "", "issuer key file, as made by 'ticketctl issuer-new' (mode 0600)")
	keyID := flag.Uint64("key-id", 0, "the issuer key's id on the chain")
	every := flag.Duration("every", 6*time.Second, "how often to look for new purchases")
	rpc := flag.String("rpc", "tcp://localhost:26657", "chain node to talk to")
	chainID := flag.String("chain-id", "smartnodes", "chain id")
	binary := flag.String("smartnodesd", "smartnodesd", "path to the smartnodesd program")
	from := flag.String("from", "", "key that pays the fees for posting signatures")
	backend := flag.String("keyring-backend", "test", "keyring backend of that key")
	home := flag.String("home", "", "smartnodesd home folder holding that key")
	gasPrices := flag.String("gas-prices", "0.025usnode", "gas price")
	flag.Parse()

	if err := run(*keyFile, *keyID, *every, &chain.CLI{
		Run: chain.Exec(*binary), RPC: *rpc, ChainID: *chainID, From: *from,
		KeyringBackend: *backend, Home: *home, GasPrices: *gasPrices,
		PollInterval: time.Second, PollTimeout: time.Minute,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "smartissuer:", err)
		os.Exit(1)
	}
}

func run(keyFile string, keyID uint64, every time.Duration, cli *chain.CLI) error {
	if keyFile == "" || cli.From == "" {
		return errors.New("-issuer and -from are required")
	}
	fi, err := os.Stat(keyFile)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is readable by others; it must be mode 0600", keyFile)
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return err
	}
	var f issuerFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("unreadable %s: %w", keyFile, err)
	}
	secret, err1 := base64.StdEncoding.DecodeString(f.SecretKey)
	public, err2 := base64.StdEncoding.DecodeString(f.PublicKey)
	if err1 != nil || err2 != nil || len(secret) == 0 || len(public) == 0 {
		return fmt.Errorf("%s does not hold an issuer key pair", keyFile)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	is, err := issuer.New(ctx, cli, keyID, secret, public)
	if err != nil {
		return err
	}
	log.Printf("signing purchases under issuer key %d; checking every %s", keyID, every)

	// Only totals are logged: purchases are public on the chain anyway, but
	// the issuer has no reason to keep a record of who bought when.
	var total issuer.Result
	lastReport := time.Now()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		r, err := is.Once(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("could not read the chain: %v", err)
		}
		total.Signed += r.Signed
		total.Tickets += r.Tickets
		total.Failed += r.Failed
		if r.Signed > 0 || r.Failed > 0 || time.Since(lastReport) > time.Hour {
			log.Printf("purchases signed %d (%d tickets); failed attempts %d", total.Signed, total.Tickets, total.Failed)
			lastReport = time.Now()
		}
		select {
		case <-ctx.Done():
			log.Printf("stopped")
			return nil
		case <-tick.C:
		}
	}
}
