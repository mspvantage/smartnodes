package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mspvantage/smartnodes/vpn/bridge"
	"github.com/mspvantage/smartnodes/vpn/control"
	"github.com/mspvantage/smartnodes/vpn/wallet"
)

// getBridge buys one bridge line, through the exit node this device is
// connected through, and saves it for when it is needed. The exit sends the
// request to the chain, so no account of this device's appears there, and
// it sees only the entry node's address. The line comes back sealed to a
// one-time key made for this request, so neither the exit nor anyone
// reading the chain can read it. The line is then checked against the
// bridge's fingerprint on the chain.
//
// Collect bridges while you can connect; use them when you cannot.
func getBridge(args []string) error {
	fs := flag.NewFlagSet("get-bridge", flag.ExitOnError)
	walletPath := fs.String("wallet", "", "ticket wallet that pays for the line")
	exitID := fs.Uint64("exit-id", 0, "the exit node this device is connected through")
	exitKey := fs.String("exit-key", "", "that exit node's public key from the registry")
	exitEndpoint := fs.String("exit-endpoint", "", "that exit node's endpoint from the registry")
	out := fs.String("out", "/var/lib/smartvpn/bridges.txt", "file the line is added to")
	wait := fs.Duration("wait", 10*time.Minute, "how long to wait for a bridge to answer")
	_ = fs.Parse(args)

	if err := needRoot(); err != nil {
		return err
	}
	if *walletPath == "" || *exitKey == "" || *exitEndpoint == "" {
		return errors.New("-wallet, -exit-key and -exit-endpoint are required")
	}
	exit, err := newPeer(*exitID, *exitKey, *exitEndpoint)
	if err != nil {
		return err
	}
	// Only through a tunnel, so the exit never sees this device's address.
	if _, dev, err := routeVia(exit.endpoint.Addr()); err != nil || (dev != outerIface && dev != innerIface) {
		return errors.New("connect first: bridge lines are bought only through a tunnel")
	}
	w, err := wallet.Open(*walletPath)
	if err != nil {
		return err
	}
	key, err := bridge.NewRequestKey()
	if err != nil {
		return err
	}
	rk := bridge.B64(key.PublicKey().Bytes())
	ticket, err := w.Spend(bridge.PoolID)
	if err != nil {
		return err
	}
	chainID := w.ChainID()
	if _, err := ask(chainID, exit, control.Hello{Op: control.OpBridgeRequest, KeyID: w.KeyID(), Tickets: []string{ticket}, RequestKey: rk}); err != nil {
		return err
	}
	fmt.Println("request sent; waiting for a bridge to answer")

	deadline := time.Now().Add(*wait)
	var sealed []byte
	for {
		r, err := ask(chainID, exit, control.Hello{Op: control.OpBridgeAnswer, RequestKey: rk})
		if err == nil {
			switch r.State {
			case 2:
				if sealed, err = bridge.UnB64(r.Sealed); err != nil {
					return errors.New("the answer is not readable")
				}
			case 3:
				return errors.New("no bridge answered in time; the ticket cannot be refunded, since tickets are anonymous")
			}
		}
		if sealed != nil {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("no answer yet; the request stays open on the chain, but this one-time key is now gone")
		}
		time.Sleep(10 * time.Second)
	}
	text, err := bridge.Open(key, sealed)
	if err != nil {
		return err
	}
	line, err := bridge.Parse(text)
	if err != nil {
		return fmt.Errorf("the bridge answered with something that is not a bridge line: %w", err)
	}
	if line.ChainID != chainID {
		return fmt.Errorf("the line is for chain %s", line.ChainID)
	}
	r, err := ask(chainID, exit, control.Hello{Op: control.OpBridgeFingerprint, NodeID: line.NodeID})
	if err != nil {
		return err
	}
	if r.Commitment != line.Fingerprint() {
		return fmt.Errorf("bridge %d's line does not match its fingerprint on the chain; not saved", line.NodeID)
	}
	if err := appendLine(*out, line.String()); err != nil {
		return err
	}
	fmt.Printf("got bridge %d; its line matches the chain and is saved in %s\n", line.NodeID, *out)
	return nil
}

// appendLine adds a line to a file readable by its owner only.
func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// readBridge turns -bridge into a line: either the line itself, or @FILE
// for a random line from a file of saved ones.
func readBridge(v string) (string, error) {
	if !strings.HasPrefix(v, "@") {
		return v, nil
	}
	raw, err := os.ReadFile(strings.TrimPrefix(v, "@"))
	if err != nil {
		return "", err
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return "", errors.New("no bridge lines in " + strings.TrimPrefix(v, "@"))
	}
	return lines[uniform(time.Duration(len(lines)-1))], nil
}
