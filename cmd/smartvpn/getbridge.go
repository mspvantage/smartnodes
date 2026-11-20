package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
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
// reading the chain can read it.
//
// The answer is then checked three ways:
//   - a second node, reached through the tunnel too, must report the same
//     sealed answer and fingerprint as the exit, so a lying exit is caught;
//   - the line must open and have the fingerprint the bridge committed to;
//   - if it does not, that is proven on the chain: the bridge's bond is
//     burned in part, it is not paid, and another bridge is asked, with a
//     fresh one-time key.
//
// The saved line keeps, beside it, what is needed to report it dead later
// ("smartvpn report-bridge"), which only the device that was given it can
// do.
//
// Collect bridges while you can connect; use them when you cannot.
func getBridge(args []string) error {
	fs := flag.NewFlagSet("get-bridge", flag.ExitOnError)
	walletPath := fs.String("wallet", "", "ticket wallet that pays for the line")
	exitID := fs.Uint64("exit-id", 0, "the exit node this device is connected through")
	exitKey := fs.String("exit-key", "", "that exit node's public key from the registry")
	exitEndpoint := fs.String("exit-endpoint", "", "that exit node's endpoint from the registry")
	checkID := fs.Uint64("check-id", 0, "a second node, not the exit, to check the answer with")
	checkKey := fs.String("check-key", "", "that node's public key from the registry")
	checkEndpoint := fs.String("check-endpoint", "", "that node's endpoint from the registry")
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
	if err := throughTunnel(exit); err != nil {
		return err
	}
	var check *peer
	if *checkKey != "" {
		c, err := newPeer(*checkID, *checkKey, *checkEndpoint)
		if err != nil {
			return err
		}
		if c.id == exit.id || c.key == exit.key {
			return errors.New("-check must be a node other than the exit")
		}
		if err := throughTunnel(c); err != nil {
			return err
		}
		check = &c
	} else {
		fmt.Println("warning: no -check node; the answer is checked through the exit only")
	}
	w, err := wallet.Open(*walletPath)
	if err != nil {
		return err
	}
	chainID := w.ChainID()

	key, err := bridge.NewRequestKey()
	if err != nil {
		return err
	}
	reportSecret := make([]byte, 32)
	if _, err := rand.Read(reportSecret); err != nil {
		return err
	}
	hash := sha256.Sum256(reportSecret)
	ticket, err := w.Spend(bridge.PoolID)
	if err != nil {
		return err
	}
	rk := bridge.B64(key.PublicKey().Bytes())
	if _, err := ask(chainID, exit, control.Hello{Op: control.OpBridgeRequest, KeyID: w.KeyID(), Tickets: []string{ticket}, RequestKey: rk, ReportHash: bridge.B64(hash[:])}); err != nil {
		return err
	}
	fmt.Println("request sent; waiting for a bridge to answer")

	// A proven rubbish answer passes the request to another bridge, under
	// a fresh key; a request is tried at most a few times.
	for attempt := 0; attempt < 4; attempt++ {
		r, err := awaitAnswer(chainID, exit, rk, *wait)
		if err != nil {
			return err
		}
		if check != nil {
			c, err := ask(chainID, *check, control.Hello{Op: control.OpBridgeAnswer, RequestKey: rk})
			if err != nil {
				return fmt.Errorf("could not check the answer with node %d: %w", check.id, err)
			}
			if c.State != r.State || c.Sealed != r.Sealed || c.Commitment != r.Commitment || c.BridgeID != r.BridgeID {
				return fmt.Errorf("exit %d and node %d disagree about the answer; one of them is lying, so nothing is saved. Use another exit", exit.id, check.id)
			}
		}
		line, why := openAnswer(key, r)
		if why == "" {
			if line.ChainID != chainID {
				return fmt.Errorf("bridge %d committed to a line for chain %s; not saved", r.BridgeID, line.ChainID)
			}
			if err := appendBridge(*out, line, rk, bridge.B64(reportSecret)); err != nil {
				return err
			}
			fmt.Printf("got bridge %d; its line matches its fingerprint on the chain and is saved in %s\n", line.NodeID, *out)
			return nil
		}

		// Rubbish. Prove it, with a fresh key for the next bridge. The proof
		// shows the old key, which opens only this rubbish answer.
		fmt.Printf("bridge %d answered with rubbish (%s); proving it on the chain\n", r.BridgeID, why)
		next, err := bridge.NewRequestKey()
		if err != nil {
			return err
		}
		via := exit
		if check != nil {
			via = *check // the exit may be the one hiding a good answer
		}
		res, err := ask(chainID, via, control.Hello{Op: control.OpBridgeReport, RequestKey: rk, Secret: bridge.B64(key.Bytes()), NewRequestKey: bridge.B64(next.PublicKey().Bytes())})
		if err != nil {
			return fmt.Errorf("could not prove the rubbish answer: %w", err)
		}
		if res.Reassigned == 0 {
			return errors.New("proven; but no other bridge is free now, and the ticket cannot be refunded since tickets are anonymous")
		}
		fmt.Printf("proven: bridge %d's bond is burned in part; bridge %d is asked instead\n", r.BridgeID, res.Reassigned)
		key, rk = next, bridge.B64(next.PublicKey().Bytes())
	}
	return errors.New("too many rubbish answers; giving up")
}

// throughTunnel refuses a node reached other than through the tunnel, so
// that it never sees this device's address.
func throughTunnel(p peer) error {
	if _, dev, err := routeVia(p.endpoint.Addr()); err != nil || (dev != outerIface && dev != innerIface) {
		return errors.New("connect first: bridge lines are bought, checked and reported only through a tunnel")
	}
	return nil
}

// awaitAnswer polls for the answer to a request.
func awaitAnswer(chainID string, via peer, rk string, wait time.Duration) (control.Reply, error) {
	deadline := time.Now().Add(wait)
	for {
		r, err := ask(chainID, via, control.Hello{Op: control.OpBridgeAnswer, RequestKey: rk})
		if err == nil {
			switch r.State {
			case 2:
				return r, nil
			case 3:
				return r, errors.New("no bridge answered in time; the ticket cannot be refunded, since tickets are anonymous")
			}
		}
		if time.Now().After(deadline) {
			return r, errors.New("no answer yet; the request stays open on the chain, but this one-time key is now gone")
		}
		time.Sleep(10 * time.Second)
	}
}

// openAnswer opens an answer and checks it. It returns why the answer is
// rubbish, or "" with the line if it is good. The checks are the chain's
// own, so a rubbish verdict here is one the chain will accept as proof.
func openAnswer(key *ecdh.PrivateKey, r control.Reply) (bridge.Line, string) {
	sealed, err := bridge.UnB64(r.Sealed)
	if err != nil {
		return bridge.Line{}, "not readable"
	}
	text, err := bridge.Open(key, sealed)
	if err != nil {
		return bridge.Line{}, "not sealed to this request"
	}
	line, err := bridge.Parse(text)
	if err != nil {
		return bridge.Line{}, "not a bridge line"
	}
	if line.Fingerprint() != r.Commitment {
		return bridge.Line{}, "not the line it committed to"
	}
	// The node id is not part of the fingerprint; the chain's is the truth.
	line.NodeID = r.BridgeID
	return line, ""
}

// reportPrefix marks the comment kept beside a saved line: the bridge, the
// request's one-time key and the secret that proves the device was given
// the line. Lines starting with # are skipped when a line is picked.
const reportPrefix = "#report "

// appendBridge saves a line with what is needed to report it dead.
func appendBridge(path string, line bridge.Line, requestKey, reportSecret string) error {
	return appendLine(path, fmt.Sprintf("%s%d %s %s\n%s", reportPrefix, line.NodeID, requestKey, reportSecret, line.String()))
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
