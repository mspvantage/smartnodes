// Command smartnode runs a SmartNodes VPN node: it takes tickets from
// clients, redeems them on chain, and then opens WireGuard sessions.
//
// It must run as root, because it creates a WireGuard interface. It keeps no
// record of any client: sessions live in memory, and its log holds totals
// only.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mspvantage/smartnodes/vpn/chain"
	"github.com/mspvantage/smartnodes/vpn/control"
	"github.com/mspvantage/smartnodes/vpn/node"
	"github.com/mspvantage/smartnodes/vpn/wg"
)

func main() {
	keyFile := flag.String("key-file", "", "file holding the node's WireGuard private key, as made by 'wg genkey'")
	nodeID := flag.Uint64("node-id", 0, "this node's id in the registry")
	chainID := flag.String("chain-id", "smartnodes", "chain id")
	listen := flag.String("listen", "0.0.0.0:51820", "address for client exchanges (TCP); its port is also the WireGuard port (UDP)")
	iface := flag.String("iface", "sn0", "WireGuard interface to create")
	subnet := flag.String("subnet", "10.77.0.0/24", "tunnel addresses handed to clients")
	session := flag.Duration("session", time.Hour, "what one ticket buys")
	gather := flag.Duration("gather", time.Second, "how long to gather arriving tickets into one redemption")
	rpc := flag.String("rpc", "tcp://localhost:26657", "chain node to talk to")
	binary := flag.String("smartnodesd", "smartnodesd", "path to the smartnodesd program")
	from := flag.String("from", "", "key that signs redemptions")
	backend := flag.String("keyring-backend", "test", "keyring backend of that key")
	home := flag.String("home", "", "smartnodesd home folder holding that key")
	gasPrices := flag.String("gas-prices", "0.025usnode", "gas price for redemptions")
	maxExchanges := flag.Int("max-exchanges", 64, "most client exchanges handled at once")
	flag.Parse()

	if err := run(*keyFile, *nodeID, *chainID, *listen, *iface, *subnet, *session, *gather, *rpc, *binary,
		*from, *backend, *home, *gasPrices, *maxExchanges); err != nil {
		log.Fatalf("smartnode: %v", err)
	}
}

func run(keyFile string, nodeID uint64, chainID, listen, iface, subnet string, session, gather time.Duration,
	rpc, binary, from, backend, home, gasPrices string, maxExchanges int) error {
	if os.Geteuid() != 0 {
		return errors.New("must run as root, to create the WireGuard interface")
	}
	if keyFile == "" || from == "" || home == "" {
		return errors.New("-key-file, -from and -home are required")
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return err
	}
	key, err := control.KeyFromBase64(strings.TrimSpace(string(raw)))
	if err != nil {
		return err
	}
	pub := base64.StdEncoding.EncodeToString(key.Public)

	listenAddr, err := netip.ParseAddrPort(listen)
	if err != nil {
		return fmt.Errorf("-listen: %w", err)
	}
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return fmt.Errorf("-subnet: %w", err)
	}

	cli := &chain.CLI{
		Run: chain.Exec(binary), RPC: rpc, ChainID: chainID, NodeID: nodeID,
		From: from, KeyringBackend: backend, Home: home, GasPrices: gasPrices,
	}

	// Refuse to start unless this server holds the key the registry lists.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	info, err := cli.Node(ctx, nodeID)
	cancel()
	if err != nil {
		return fmt.Errorf("reading node %d from the registry: %w", nodeID, err)
	}
	if info.Pubkey != pub {
		return fmt.Errorf("this server's key %s does not match node %d's key in the registry", pub, nodeID)
	}
	if info.Status != 1 {
		return fmt.Errorf("node %d is not active in the registry", nodeID)
	}

	n, err := node.New(node.Config{
		ChainID: chainID, NodeID: nodeID, Subnet: prefix, SessionLength: session,
		RedeemMargin: time.Hour, MaxBatch: 100, Gather: gather,
	}, cli, wg.Interface{Name: iface, Run: wg.Exec{}})
	if err != nil {
		return err
	}

	tunnel := wg.Interface{Name: iface, Run: wg.Exec{}}
	if err := tunnel.SetUpServer(key.Private, listenAddr.Port(), n.Address()); err != nil {
		return err
	}
	// Removing the interface forgets every session at once.
	defer func() { _ = tunnel.Delete() }()

	ln, err := net.Listen("tcp", listenAddr.String())
	if err != nil {
		return err
	}

	stop, stopNow := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopNow()

	log.Printf("node %d serving on %s; public key %s; one ticket buys %s", nodeID, listenAddr, pub, session)

	var failed atomic.Uint64
	slots := make(chan struct{}, maxExchanges)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
				go func() {
					defer func() { <-slots }()
					defer conn.Close()
					// The client's address is never passed on or logged.
					if err := control.Serve(conn, chainID, nodeID, key, n.HandleHello); err != nil {
						failed.Add(1)
					}
				}()
			default:
				conn.Close()
			}
		}
	}()

	go n.Run(stop)

	expiry := time.NewTicker(5 * time.Second)
	report := time.NewTicker(time.Minute)
	defer expiry.Stop()
	defer report.Stop()
	for {
		select {
		case <-expiry.C:
			n.Expire()
		case <-report.C:
			logStats(n, failed.Load())
		case <-stop.Done():
			ln.Close()
			logStats(n, failed.Load())
			log.Printf("stopped; every session is forgotten")
			return nil
		}
	}
}

// logStats writes the node's totals. Nothing here identifies a client.
func logStats(n *node.Node, failed uint64) {
	s := n.Stats()
	log.Printf("sessions open %d; tickets accepted %d, refused %d, redeemed %d, unconfirmed %d; failed exchanges %d",
		s.Open, s.Accepted, s.Refused, s.Redeemed, s.Unconfirmed, failed)
}
