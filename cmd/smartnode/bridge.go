package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mspvantage/smartnodes/vpn/bridge"
	"github.com/mspvantage/smartnodes/vpn/chain"
	"github.com/mspvantage/smartnodes/vpn/control"
	"github.com/mspvantage/smartnodes/vpn/disguise"
)

// makeLine builds this node's bridge line.
func makeLine(chainID string, nodeID uint64, wgPub string, listen netip.AddrPort, ro *realityOpts) (bridge.Line, error) {
	addr, err := netip.ParseAddr(ro.bridge)
	if err != nil {
		return bridge.Line{}, fmt.Errorf("-bridge-address: %w", err)
	}
	if !listen.Addr().IsLoopback() {
		return bridge.Line{}, errors.New("a bridge must -listen on 127.0.0.1, so only its disguise reaches it")
	}
	_, pub, err := ro.published()
	if err != nil {
		return bridge.Line{}, err
	}
	l := bridge.Line{ChainID: chainID, NodeID: nodeID, Address: addr, Port: listen.Port(), WGKey: wgPub, Reality: pub}
	return l, l.Validate()
}

// saveLine keeps the bridge line where only root can read it: anyone with
// it can find this bridge.
func saveLine(dir string, l bridge.Line) (string, error) {
	return disguise.WriteConfig(dir, "bridge-line.txt", []byte(l.String()+"\n"))
}

// bridgeLineCmd prints what an operator needs to register a bridge, or,
// with -node-id, the bridge line to hand out.
func bridgeLineCmd(args []string) error {
	fs := flag.NewFlagSet("bridge-line", flag.ExitOnError)
	keyFile := fs.String("key-file", "", "the node's WireGuard private key file")
	chainID := fs.String("chain-id", "smartnodes", "chain id")
	nodeID := fs.Int64("node-id", -1, "the bridge's id, once registered")
	listen := fs.String("listen", "127.0.0.1:51820", "the node's own address and port; a bridge listens on 127.0.0.1")
	var ro realityOpts
	fs.UintVar(&ro.port, "reality-port", 443, "the disguise's TCP port")
	fs.StringVar(&ro.cover, "reality-cover", "www.microsoft.com:443", "the real website whose handshake the disguise passes along")
	fs.StringVar(&ro.serverName, "reality-server-name", "", "the cover site's name (default: the cover's host)")
	fs.StringVar(&ro.dir, "reality-dir", "/var/lib/smartnode", "folder for the disguise's keys")
	fs.StringVar(&ro.bridge, "bridge-address", "", "this bridge's own public IPv4 address")
	_ = fs.Parse(args)

	if *keyFile == "" || ro.bridge == "" {
		return errors.New("-key-file and -bridge-address are required")
	}
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	key, err := control.KeyFromBase64(strings.TrimSpace(string(raw)))
	if err != nil {
		return err
	}
	la, err := netip.ParseAddrPort(*listen)
	if err != nil {
		return fmt.Errorf("-listen: %w", err)
	}
	id := uint64(0)
	if *nodeID >= 0 {
		id = uint64(*nodeID)
	}
	l, err := makeLine(*chainID, id, base64.StdEncoding.EncodeToString(key.Public), la, &ro)
	if err != nil {
		return err
	}
	if *nodeID < 0 {
		fmt.Printf("fingerprint: %s\n", l.Fingerprint())
		fmt.Printf("register the bridge with:\n  smartnodesd tx registry register-bridge %s <bond, at least the entry minimum> --from <operator>\n", l.Fingerprint())
		fmt.Printf("then run this again with -node-id set to the new bridge's id, to get the bridge line\n")
		return nil
	}
	path, err := saveLine(ro.dir, l)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", l)
	fmt.Fprintf(os.Stderr, "saved in %s (readable by its owner only); give it only to people you choose: anyone with it can find this bridge\n", filepath.Clean(path))
	return nil
}

// answerBridgeRequests answers, every few seconds, the paid requests the
// chain has assigned to this bridge: its line, sealed to each requester's
// one-time key. Only totals are logged.
func answerBridgeRequests(ctx context.Context, cli *chain.CLI, line bridge.Line) {
	answered := map[uint64]bool{}
	total := 0
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	var lastCheck time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if time.Since(lastCheck) > 10*time.Minute {
			lastCheck = time.Now()
			qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			st, err := cli.BridgeLine(qctx, line.NodeID)
			cancel()
			switch {
			case err != nil:
			case st.Stopped:
				log.Printf("MOVE THIS BRIDGE: %d of the %d people given its line reported it not working, so it is no longer handed out. "+
					"Change its port (or address) and run update-bridge; the new line is handed out again", st.Dead, st.Given)
			case st.Full:
				log.Printf("this bridge's line has been given to %d people, the most allowed; move it (new port, update-bridge) to be handed out again", st.Given)
			}
		}
		qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		reqs, err := cli.BridgeRequestsFor(qctx, line.NodeID)
		cancel()
		if err != nil {
			continue
		}
		for _, r := range reqs {
			if answered[r.ID] {
				continue
			}
			key, err := bridge.UnB64(r.RequestKey)
			if err != nil {
				answered[r.ID] = true
				continue
			}
			sealed, err := bridge.Seal(key, line.String())
			if err != nil {
				answered[r.ID] = true
				continue
			}
			actx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			err = cli.AnswerBridge(actx, r.ID, bridge.B64(sealed))
			cancel()
			if err != nil {
				log.Printf("could not answer a bridge request: %v", err)
				continue
			}
			answered[r.ID] = true
			total++
			log.Printf("bridge requests answered: %d", total)
		}
	}
}
