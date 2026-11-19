// Command smartvpn is the SmartNodes client.
//
// connect uses two hops. The client pays an entry node and opens a tunnel to
// it, then opens a second tunnel inside the first to an exit node. The entry
// node sees the client's address but not where its traffic goes; the exit
// node sees where traffic goes but only the entry node's address.
//
// The exit is paid ahead, through a tunnel, at a time unrelated to when the
// session opens: 'smartvpn prepay' buys a credit and saves it to a file, and
// a later connect uses it with no step on the chain. Without a saved credit,
// connect pays the exit and then waits a random time before using it.
//
// It must run as root, because it creates WireGuard interfaces and changes
// routes.
//
//	smartvpn connect -entry-id 0 -entry-key K -entry-endpoint H:P -entry-ticket T \
//	                 -exit-id 1 -exit-key K -exit-endpoint H:P -exit-ticket T -key-id 0
//	smartvpn connect ... -exit-credit /root/.smartvpn/credit.json
//	smartvpn prepay -exit-id 1 -exit-key K -exit-endpoint H:P -key-id 0 -ticket T -out FILE
//	smartvpn up ...      (one hop, as in stage 1)
//	smartvpn down
package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/mspvantage/smartnodes/vpn/control"
	"github.com/mspvantage/smartnodes/vpn/wg"
)

const (
	outerIface = "sv0"
	innerIface = "sv1"
	// innerMTU leaves room for the outer tunnel's 60 bytes inside a 1420
	// byte WireGuard MTU, with margin for PPPoE and similar links.
	innerMTU = 1340
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "connect":
		err = connect(os.Args[2:])
	case "prepay":
		err = prepay(os.Args[2:])
	case "up":
		err = up(os.Args[2:])
	case "down":
		err = down()
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "smartvpn:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  smartvpn connect -entry-id N -entry-key KEY -entry-endpoint HOST:PORT -entry-ticket T -key-id K
                   (-exit-id N -exit-key KEY -exit-endpoint HOST:PORT -exit-ticket T [-wait 10m] | -exit-credit FILE)
  smartvpn prepay  -exit-id N -exit-key KEY -exit-endpoint HOST:PORT -key-id K -ticket T -out FILE
  smartvpn up      -node-id N -node-key KEY -endpoint HOST:PORT -key-id K -ticket T
  smartvpn down
All take -chain-id ID (default smartnodes).`)
	os.Exit(2)
}

func needRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("must run as root, to create WireGuard interfaces")
	}
	return nil
}

// peer is a node the client talks to.
type peer struct {
	id       uint64
	key      string
	pub      []byte
	endpoint netip.AddrPort
}

func newPeer(id uint64, key, endpoint string) (peer, error) {
	pub, err := control.DecodePublicKey(key)
	if err != nil {
		return peer{}, err
	}
	udp, err := net.ResolveUDPAddr("udp4", endpoint)
	if err != nil {
		return peer{}, fmt.Errorf("endpoint %s: %w", endpoint, err)
	}
	ap := udp.AddrPort()
	return peer{id: id, key: key, pub: pub, endpoint: netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())}, nil
}

// ask runs one exchange with a node.
func ask(chainID string, p peer, h control.Hello) (control.Reply, error) {
	conn, err := net.DialTimeout("tcp", p.endpoint.String(), control.Timeout)
	if err != nil {
		return control.Reply{}, fmt.Errorf("cannot reach node %d: %w", p.id, err)
	}
	defer conn.Close()
	reply, err := control.Dial(conn, chainID, p.id, p.pub, h)
	if err != nil {
		return reply, err
	}
	if !reply.OK {
		return reply, fmt.Errorf("node %d refused: %s", p.id, reply.Error)
	}
	return reply, nil
}

func tunnelAddr(r control.Reply) (netip.Addr, error) {
	addr, err := netip.ParseAddr(r.TunnelIP)
	if err != nil || !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("the node gave an unusable tunnel address %q", r.TunnelIP)
	}
	return addr, nil
}

func iface(name string) wg.Interface { return wg.Interface{Name: name, Run: wg.Exec{}} }

// randomWait picks a time between a fifth of max and max.
func randomWait(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	low := max / 5
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-low)+1))
	if err != nil {
		return max
	}
	return low + time.Duration(n.Int64())
}

func connect(args []string) (err error) {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	chainID := fs.String("chain-id", "smartnodes", "chain id")
	keyID := fs.Uint64("key-id", 0, "issuer key the tickets were signed under")
	entryID := fs.Uint64("entry-id", 0, "entry node id")
	entryKey := fs.String("entry-key", "", "entry node's public key from the registry")
	entryEndpoint := fs.String("entry-endpoint", "", "entry node's endpoint from the registry")
	entryTicket := fs.String("entry-ticket", "", "ticket spent to the entry node")
	exitID := fs.Uint64("exit-id", 0, "exit node id")
	exitKey := fs.String("exit-key", "", "exit node's public key from the registry")
	exitEndpoint := fs.String("exit-endpoint", "", "exit node's endpoint from the registry")
	exitTicket := fs.String("exit-ticket", "", "ticket spent to the exit node, when no credit is saved")
	exitCredit := fs.String("exit-credit", "", "credit file from 'smartvpn prepay'")
	wait := fs.Duration("wait", 10*time.Minute, "with -exit-ticket, the longest random wait between paying the exit and using it")
	_ = fs.Parse(args)

	if err := needRoot(); err != nil {
		return err
	}
	if *entryKey == "" || *entryEndpoint == "" || *entryTicket == "" {
		return errors.New("-entry-key, -entry-endpoint and -entry-ticket are required")
	}
	if (*exitCredit == "") == (*exitTicket == "") {
		return errors.New("give either -exit-credit or -exit-ticket")
	}
	entry, err := newPeer(*entryID, *entryKey, *entryEndpoint)
	if err != nil {
		return err
	}

	var exit peer
	var exitPriv []byte
	if *exitCredit != "" {
		c, err := loadCredit(*exitCredit, time.Now())
		if err != nil {
			return err
		}
		if c.ChainID != *chainID {
			return fmt.Errorf("the credit is for chain %s", c.ChainID)
		}
		if exit, err = newPeer(c.ExitID, c.ExitKey, c.Endpoint); err != nil {
			return err
		}
		if exitPriv, err = base64.StdEncoding.DecodeString(c.WGPrivate); err != nil || len(exitPriv) != 32 {
			return errors.New("the credit file holds a bad key")
		}
	} else {
		if *exitKey == "" || *exitEndpoint == "" {
			return errors.New("-exit-key and -exit-endpoint are required with -exit-ticket")
		}
		if exit, err = newPeer(*exitID, *exitKey, *exitEndpoint); err != nil {
			return err
		}
	}
	if exit.endpoint.Addr() == entry.endpoint.Addr() {
		return errors.New("the entry and exit nodes are on the same address")
	}

	if err := prepareRoutes(entry.endpoint.Addr()); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = iface(innerIface).Delete()
			_ = iface(outerIface).Delete()
			_ = restoreRoutes()
		}
	}()

	// Hop one: the entry node.
	outerKey, err := control.NewKey()
	if err != nil {
		return err
	}
	reply, err := ask(*chainID, entry, control.Hello{
		KeyID: *keyID, Tickets: strings.Fields(*entryTicket), WGPubkey: base64.StdEncoding.EncodeToString(outerKey.Public),
	})
	if err != nil {
		return err
	}
	outerAddr, err := tunnelAddr(reply)
	if err != nil {
		return err
	}
	// The outer tunnel carries only traffic for the exit node.
	if err := iface(outerIface).SetUpTunnel(wg.Tunnel{
		PrivateKey: outerKey.Private, NodeKey: entry.key, Endpoint: entry.endpoint, Address: outerAddr,
		Allowed: []netip.Prefix{netip.PrefixFrom(exit.endpoint.Addr(), 32)},
	}); err != nil {
		return err
	}
	// Until the inner tunnel is up, everything else is sent to the outer
	// interface, which drops it: nothing leaves outside the tunnels.
	if err := run("ip", "route", "replace", "default", "dev", outerIface); err != nil {
		return err
	}
	fmt.Printf("entry node %d: tunnel address %s\n", entry.id, outerAddr)

	// Hop two: the exit node, reached through the entry node.
	if exitPriv == nil {
		k, err := control.NewKey()
		if err != nil {
			return err
		}
		exitPriv = k.Private
		if _, err := ask(*chainID, exit, control.Hello{
			KeyID: *keyID, Tickets: strings.Fields(*exitTicket), WGPubkey: base64.StdEncoding.EncodeToString(k.Public), Prepay: true,
		}); err != nil {
			return err
		}
		d := randomWait(*wait).Round(time.Second)
		fmt.Printf("exit node %d paid; waiting %s before using it, so the chain cannot link the two\n", exit.id, d)
		time.Sleep(d)
	}
	exitKeyPair, err := control.KeyFromPrivate(exitPriv)
	if err != nil {
		return err
	}
	reply, err = ask(*chainID, exit, control.Hello{KeyID: *keyID, WGPubkey: base64.StdEncoding.EncodeToString(exitKeyPair.Public)})
	if err != nil {
		return err
	}
	if *exitCredit != "" {
		// A credit is used once.
		_ = os.Remove(*exitCredit)
	}
	innerAddr, err := tunnelAddr(reply)
	if err != nil {
		return err
	}
	if err := iface(innerIface).SetUpTunnel(wg.Tunnel{
		PrivateKey: exitPriv, NodeKey: exit.key, Endpoint: exit.endpoint, Address: innerAddr,
		Allowed: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, MTU: innerMTU,
	}); err != nil {
		return err
	}
	fmt.Printf("exit node %d: tunnel address %s; session ends %s\nconnected through two hops\n",
		exit.id, innerAddr, time.Unix(reply.Expires, 0).Format("15:04:05"))
	return nil
}

func prepay(args []string) error {
	fs := flag.NewFlagSet("prepay", flag.ExitOnError)
	chainID := fs.String("chain-id", "smartnodes", "chain id")
	keyID := fs.Uint64("key-id", 0, "issuer key the tickets were signed under")
	exitID := fs.Uint64("exit-id", 0, "exit node id")
	exitKey := fs.String("exit-key", "", "exit node's public key from the registry")
	exitEndpoint := fs.String("exit-endpoint", "", "exit node's endpoint from the registry")
	ticket := fs.String("ticket", "", "tickets spent to the exit node")
	out := fs.String("out", "", "file to save the credit to")
	_ = fs.Parse(args)

	if err := needRoot(); err != nil {
		return err
	}
	if *exitKey == "" || *exitEndpoint == "" || *ticket == "" || *out == "" {
		return errors.New("-exit-key, -exit-endpoint, -ticket and -out are required")
	}
	exit, err := newPeer(*exitID, *exitKey, *exitEndpoint)
	if err != nil {
		return err
	}
	// Only through a tunnel, so the exit node never sees this machine's
	// own address.
	_, dev, err := routeVia(exit.endpoint.Addr())
	if err != nil {
		return err
	}
	if dev != outerIface && dev != innerIface {
		return errors.New("connect first: a prepayment is only sent through a tunnel")
	}
	k, err := control.NewKey()
	if err != nil {
		return err
	}
	reply, err := ask(*chainID, exit, control.Hello{
		KeyID: *keyID, Tickets: strings.Fields(*ticket), WGPubkey: base64.StdEncoding.EncodeToString(k.Public), Prepay: true,
	})
	if err != nil {
		return err
	}
	if err := saveCredit(*out, creditFile{
		ChainID: *chainID, ExitID: exit.id, ExitKey: exit.key, Endpoint: exit.endpoint.String(),
		WGPrivate: base64.StdEncoding.EncodeToString(k.Private), Expires: reply.Expires,
	}); err != nil {
		return err
	}
	fmt.Printf("credit saved for exit node %d; use it before %s\n", exit.id, time.Unix(reply.Expires, 0).Format(time.RFC3339))
	return nil
}

// up is the one-hop connection from stage 1.
func up(args []string) (err error) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	nodeID := fs.Uint64("node-id", 0, "id of the node to use")
	nodeKey := fs.String("node-key", "", "the node's public key from the registry")
	endpoint := fs.String("endpoint", "", "the node's endpoint from the registry")
	chainID := fs.String("chain-id", "smartnodes", "chain id")
	keyID := fs.Uint64("key-id", 0, "issuer key the ticket was signed under")
	ticket := fs.String("ticket", "", "a ticket spent to this node")
	_ = fs.Parse(args)

	if err := needRoot(); err != nil {
		return err
	}
	if *nodeKey == "" || *endpoint == "" || *ticket == "" {
		return errors.New("-node-key, -endpoint and -ticket are required")
	}
	p, err := newPeer(*nodeID, *nodeKey, *endpoint)
	if err != nil {
		return err
	}
	if err := prepareRoutes(p.endpoint.Addr()); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = iface(outerIface).Delete()
			_ = restoreRoutes()
		}
	}()
	k, err := control.NewKey()
	if err != nil {
		return err
	}
	reply, err := ask(*chainID, p, control.Hello{
		KeyID: *keyID, Tickets: strings.Fields(*ticket), WGPubkey: base64.StdEncoding.EncodeToString(k.Public),
	})
	if err != nil {
		return err
	}
	addr, err := tunnelAddr(reply)
	if err != nil {
		return err
	}
	if err := iface(outerIface).SetUpClient(k.Private, p.key, p.endpoint, addr); err != nil {
		return err
	}
	fmt.Printf("connected through node %d; tunnel address %s; session ends %s\n",
		p.id, addr, time.Unix(reply.Expires, 0).Format("15:04:05"))
	return nil
}

func down() error {
	if err := needRoot(); err != nil {
		return err
	}
	_ = iface(innerIface).Delete()
	_ = iface(outerIface).Delete()
	if err := restoreRoutes(); err != nil {
		return err
	}
	fmt.Println("disconnected")
	return nil
}
