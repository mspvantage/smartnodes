// Command smartnode runs a SmartNodes VPN node: it takes tickets from
// clients, redeems them on chain, and then opens WireGuard sessions.
//
// Its role comes from its registry tier. An entry node passes its clients'
// traffic only to registered exit nodes; an exit node passes it to the
// internet, and also sells exit credits paid ahead. Firewall rules enforce
// both (package guard). The exit machine must also route and masquerade the
// tunnel subnet to the internet, as for any VPN server.
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
	"github.com/mspvantage/smartnodes/vpn/guard"
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
	if info.Status != chain.StatusActive {
		return fmt.Errorf("node %d is not active in the registry", nodeID)
	}
	var role guard.Role
	switch info.Tier {
	case chain.TierEntry:
		role = guard.Entry
	case chain.TierExit:
		role = guard.Exit
	default:
		return fmt.Errorf("node %d has unknown tier %d", nodeID, info.Tier)
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

	// The rules go in before the first client can connect. An entry node
	// starts with no exits allowed and learns them from the registry.
	fw := guard.Guard{Iface: iface, Run: wg.Exec{}}
	if err := fw.Apply(role, nil); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	defer func() { _ = fw.Remove() }()
	exits := 0
	refreshExits := func() {
		if role != guard.Entry {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		allowed, err := exitEndpoints(ctx, cli)
		if err != nil {
			// Keep the last good list.
			log.Printf("could not refresh exit nodes: %v", err)
			return
		}
		if err := fw.Apply(role, allowed); err != nil {
			log.Printf("could not update firewall: %v", err)
			return
		}
		exits = len(allowed)
	}
	refreshExits()

	ln, err := net.Listen("tcp", listenAddr.String())
	if err != nil {
		return err
	}

	stop, stopNow := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopNow()

	log.Printf("node %d (%s) serving on %s; public key %s; one ticket buys %s", nodeID, roleName(role), listenAddr, pub, session)
	if role == guard.Entry {
		log.Printf("forwarding to %d exit endpoints", exits)
	}

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
	registry := time.NewTicker(time.Minute)
	defer expiry.Stop()
	defer report.Stop()
	defer registry.Stop()
	for {
		select {
		case <-expiry.C:
			n.Expire()
		case <-registry.C:
			refreshExits()
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

func roleName(r guard.Role) string {
	if r == guard.Entry {
		return "entry"
	}
	return "exit"
}

// exitEndpoints lists the IPv4 endpoints of active exit nodes.
func exitEndpoints(ctx context.Context, cli *chain.CLI) ([]netip.AddrPort, error) {
	nodes, err := cli.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[netip.AddrPort]bool{}
	var out []netip.AddrPort
	for _, nd := range nodes {
		if nd.Tier != chain.TierExit || nd.Status != chain.StatusActive {
			continue
		}
		ap, err := resolve(ctx, nd.Endpoint)
		if err != nil || seen[ap] {
			continue
		}
		seen[ap] = true
		out = append(out, ap)
	}
	return out, nil
}

// resolve turns host:port into an IPv4 address and port.
func resolve(ctx context.Context, endpoint string) (netip.AddrPort, error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return netip.AddrPort{}, err
	}
	p, err := net.DefaultResolver.LookupPort(ctx, "udp", port)
	if err != nil {
		return netip.AddrPort{}, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return netip.AddrPort{}, fmt.Errorf("no IPv4 address for %s", host)
	}
	return netip.AddrPortFrom(ips[0].Unmap(), uint16(p)), nil
}

// logStats writes the node's totals. Nothing here identifies a client.
func logStats(n *node.Node, failed uint64) {
	s := n.Stats()
	log.Printf("sessions open %d; credits held %d; tickets accepted %d, prepaid %d, refused %d, redeemed %d, unconfirmed %d; credits used %d; failed exchanges %d",
		s.Open, s.Credits, s.Accepted, s.Prepaid, s.Refused, s.Redeemed, s.Unconfirmed, s.CreditsUsed, failed)
}
