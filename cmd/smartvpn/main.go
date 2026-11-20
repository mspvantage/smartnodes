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
// run is the everyday command. It pays from a ticket wallet, keeps both
// sessions renewed at independent random times, buys the next session's exit
// credit ahead, and falls back to buying a fresh one if a saved credit is
// refused. While connected a kill switch lets nothing out except through the
// tunnels, blocks IPv6, and stays on if a tunnel or the program dies, until
// 'smartvpn down'. Name lookups go to the exit's own name server.
//
// It must run as root, because it creates WireGuard interfaces and changes
// routes and firewall rules.
//
//	smartvpn run -wallet W -entry-id 0 -entry-key K -entry-endpoint H:P -exit-id 1 -exit-key K -exit-endpoint H:P
//	smartvpn connect -entry-id 0 -entry-key K -entry-endpoint H:P -entry-ticket T \
//	                 -exit-id 1 -exit-key K -exit-endpoint H:P -exit-ticket T -key-id 0
//	smartvpn connect ... -exit-credit /root/.smartvpn/credit.json
//	smartvpn prepay -exit-id 1 -exit-key K -exit-endpoint H:P -key-id 0 -ticket T -out FILE
//	smartvpn up ...      (one hop, as in stage 1)
//	smartvpn down
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mspvantage/smartnodes/vpn/bridge"
	"github.com/mspvantage/smartnodes/vpn/control"
	"github.com/mspvantage/smartnodes/vpn/disguise"
	"github.com/mspvantage/smartnodes/vpn/guard"
	"github.com/mspvantage/smartnodes/vpn/wallet"
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
	case "run":
		err = runCmd(os.Args[2:])
	case "get-bridge":
		err = getBridge(os.Args[2:])
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
  smartvpn run     -wallet FILE -entry-id N -entry-key KEY -entry-endpoint HOST:PORT
                   -exit-id N -exit-key KEY -exit-endpoint HOST:PORT [-credit-dir DIR] [-wait 10m]
  smartvpn connect -entry-id N -entry-key KEY -entry-endpoint HOST:PORT -entry-ticket T -key-id K
                   (-exit-id N -exit-key KEY -exit-endpoint HOST:PORT -exit-ticket T [-wait 10m] | -exit-credit FILE)
  smartvpn get-bridge -wallet FILE -exit-id N -exit-key KEY -exit-endpoint HOST:PORT [-out FILE]
  smartvpn prepay  -exit-id N -exit-key KEY -exit-endpoint HOST:PORT -key-id K -ticket T -out FILE
  smartvpn up      -node-id N -node-key KEY -endpoint HOST:PORT -key-id K -ticket T
  smartvpn down
The others take -chain-id ID (default smartnodes); run reads it from the wallet.`)
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
		return reply, fmt.Errorf("node %d did not answer properly: %w", p.id, err)
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

// uniform picks a time from 0 to max, from the system's secure source.
func uniform(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)+1))
	if err != nil {
		return max
	}
	return time.Duration(n.Int64())
}

// randomWait picks a time between a fifth of max and max.
func randomWait(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return max/5 + uniform(max-max/5)
}

func connect(args []string) error {
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
	exitTicket := fs.String("exit-ticket", "", "ticket spent to the exit node, used if there is no usable credit")
	exitCredit := fs.String("exit-credit", "", "credit file from 'smartvpn prepay'")
	wait := fs.Duration("wait", 10*time.Minute, "the longest random wait between paying the exit and using it")
	tr := addTransportFlags(fs, "wireguard")
	_ = fs.Parse(args)

	if err := needRoot(); err != nil {
		return err
	}
	if *entryTicket == "" || (*exitCredit == "" && *exitTicket == "") {
		return errors.New("-entry-ticket, and -exit-credit or -exit-ticket, are required")
	}
	if *exitCredit != "" && *exitKey == "" {
		// The exit comes from the credit file.
		cr, err := loadCredit(*exitCredit, time.Now())
		if err != nil {
			return err
		}
		*exitID, *exitKey, *exitEndpoint = cr.ExitID, cr.ExitKey, cr.Endpoint
	}
	tickets := map[uint64][]string{*entryID: {*entryTicket}}
	if *exitTicket != "" {
		tickets[*exitID] = append(tickets[*exitID], *exitTicket)
	}
	pay := func(nodeID uint64) (string, error) {
		if len(tickets[nodeID]) == 0 {
			return "", fmt.Errorf("no ticket given for node %d", nodeID)
		}
		t := tickets[nodeID][0]
		tickets[nodeID] = tickets[nodeID][1:]
		return t, nil
	}
	c, err := newClient(*chainID, *keyID, *entryID, *entryKey, *entryEndpoint, *exitID, *exitKey, *exitEndpoint, pay, *exitCredit, *wait, tr)
	if err != nil {
		return err
	}
	return c.open()
}

// transportOpts say how the entry is reached.
type transportOpts struct {
	transport string
	reality   string // compact form, from the registry
	xray      string
	bridge    string // a bridge line, in place of the entry's details
	// memory is a file remembering that WireGuard was blocked on this
	// network; empty means do not remember.
	memory string
}

func addTransportFlags(fs *flag.FlagSet, def string) *transportOpts {
	o := &transportOpts{}
	fs.StringVar(&o.transport, "transport", def, "how to reach the entry node: wireguard, reality (disguised), or auto (WireGuard, then the disguise if it is blocked)")
	fs.StringVar(&o.reality, "entry-reality", "", "the entry node's disguise from the registry, as PORT/PUBLICKEY/SHORTID/SERVERNAME/UUID")
	fs.StringVar(&o.xray, "xray", "xray", "path to the Xray program, for the disguise")
	fs.StringVar(&o.bridge, "bridge", "", "a bridge line, or @FILE for one of the lines saved in FILE, used as the entry in place of the -entry flags")
	return o
}

func newClient(chainID string, keyID, entryID uint64, entryKey, entryEndpoint string, exitID uint64, exitKey, exitEndpoint string,
	pay func(uint64) (string, error), creditPath string, wait time.Duration, t *transportOpts) (*client, error) {
	var line *bridge.Line
	if t.bridge != "" {
		text, err := readBridge(t.bridge)
		if err != nil {
			return nil, err
		}
		l, err := bridge.Parse(text)
		if err != nil {
			return nil, err
		}
		if l.ChainID != chainID {
			return nil, fmt.Errorf("the bridge line is for chain %s", l.ChainID)
		}
		line = &l
		entryID, entryKey = l.NodeID, l.WGKey
		entryEndpoint = netip.AddrPortFrom(l.Address, l.Port).String()
		t.transport = "reality"
		t.reality = l.Reality.Compact()
	}
	if entryKey == "" || entryEndpoint == "" || exitKey == "" || exitEndpoint == "" {
		return nil, errors.New("the entry's and exit's keys and endpoints are required")
	}
	entry, err := newPeer(entryID, entryKey, entryEndpoint)
	if err != nil {
		return nil, err
	}
	exit, err := newPeer(exitID, exitKey, exitEndpoint)
	if err != nil {
		return nil, err
	}
	if exit.endpoint.Addr() == entry.endpoint.Addr() {
		return nil, errors.New("the entry and exit nodes are on the same address")
	}
	c := &client{
		chainID: chainID, keyID: keyID, entry: entry, direct: entry, exit: exit, pay: pay, creditPath: creditPath, wait: wait,
		say:       func(format string, a ...any) { fmt.Printf(time.Now().Format("15:04:05 ")+format+"\n", a...) },
		transport: t.transport, xray: t.xray, memory: t.memory,
	}
	c.target = entry.endpoint
	if line != nil {
		// A bridge's own port is reached on its loopback address, through
		// its disguise.
		c.target = line.Inner()
		c.say("entry is bridge %d, reached only through its disguise", line.NodeID)
	}
	switch t.transport {
	case "wireguard", "reality", "auto":
	default:
		return nil, fmt.Errorf("-transport %q: want wireguard, reality or auto", t.transport)
	}
	if t.reality != "" {
		r, err := disguise.ParseCompact(t.reality)
		if err != nil {
			return nil, err
		}
		c.reality = &r
	}
	if t.transport == "reality" && c.reality == nil {
		return nil, errors.New("-transport reality needs -entry-reality")
	}
	return c, nil
}

// runCmd connects, then stays running: it renews both sessions from the
// wallet, buys the next session's exit credit ahead, and disconnects
// cleanly when stopped.
func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	walletPath := fs.String("wallet", "", "ticket wallet file")
	entryID := fs.Uint64("entry-id", 0, "entry node id")
	entryKey := fs.String("entry-key", "", "entry node's public key from the registry")
	entryEndpoint := fs.String("entry-endpoint", "", "entry node's endpoint from the registry")
	exitID := fs.Uint64("exit-id", 0, "exit node id")
	exitKey := fs.String("exit-key", "", "exit node's public key from the registry")
	exitEndpoint := fs.String("exit-endpoint", "", "exit node's endpoint from the registry")
	creditDir := fs.String("credit-dir", "/var/lib/smartvpn", "folder for exit credits bought ahead")
	wait := fs.Duration("wait", 10*time.Minute, "the longest random wait between paying an exit and first using it")
	tr := addTransportFlags(fs, "auto")
	_ = fs.Parse(args)

	if err := needRoot(); err != nil {
		return err
	}
	if *walletPath == "" {
		return errors.New("-wallet is required")
	}
	w, err := wallet.Open(*walletPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*creditDir, 0o700); err != nil {
		return err
	}
	creditPath := filepath.Join(*creditDir, fmt.Sprintf("credit-%d.json", *exitID))
	tr.memory = filepath.Join(*creditDir, "transport.json")
	c, err := newClient(w.ChainID(), w.KeyID(), *entryID, *entryKey, *entryEndpoint, *exitID, *exitKey, *exitEndpoint, w.Spend, creditPath, *wait, tr)
	if err != nil {
		return err
	}

	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := c.open(); err != nil {
		return err
	}
	c.say("%d tickets left in the wallet", w.Left())

	now := time.Now()
	entryAt := renewAt(now, c.entryEnds)
	exitAt := renewAt(now, c.exitEnds)
	var creditAt time.Time
	if _, err := os.Stat(creditPath); err != nil {
		creditAt = now.Add(randomBetween(c.exitEnds.Sub(now)/10, c.exitEnds.Sub(now)*9/10))
	}
	retry := 30 * time.Second
	for {
		next := entryAt
		if exitAt.Before(next) {
			next = exitAt
		}
		if !creditAt.IsZero() && creditAt.Before(next) {
			next = creditAt
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-stop.Done():
			timer.Stop()
			c.say("stopping")
			if err := teardown(); err != nil {
				return err
			}
			c.say("disconnected; this machine is back as it was")
			return nil
		case <-timer.C:
		}
		now = time.Now()
		switch {
		case !now.Before(entryAt):
			if err := c.renewEntry(); err != nil {
				c.say("could not renew the entry session (%v); retrying in %s; the kill switch stays on", err, retry)
				entryAt = now.Add(retry)
				continue
			}
			entryAt = renewAt(now, c.entryEnds)
			c.say("entry session renewed; it now ends %s; %d tickets left", clock(c.entryEnds), w.Left())
		case !now.Before(exitAt):
			if err := c.renewExit(); err != nil {
				c.say("could not renew the exit session (%v); retrying in %s; the kill switch stays on", err, retry)
				exitAt = now.Add(retry)
				continue
			}
			exitAt = renewAt(now, c.exitEnds)
			c.say("exit session renewed; it now ends %s; %d tickets left", clock(c.exitEnds), w.Left())
		default:
			if err := c.buyCredit(); err != nil {
				c.say("could not buy the next exit credit (%v); retrying in %s", err, retry)
				creditAt = now.Add(retry)
				continue
			}
			creditAt = time.Time{}
			c.say("exit credit for the next connection bought ahead; %d tickets left", w.Left())
		}
	}
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

// up is the one-hop connection from stage 1, with the kill switch.
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
			teardown()
		}
	}()
	if err := (guard.KillSwitch{Run: wg.Exec{}, Tunnels: []string{outerIface}}).Apply(p.endpoint); err != nil {
		return fmt.Errorf("kill switch: %w", err)
	}
	k, err := control.NewKey()
	if err != nil {
		return err
	}
	reply, err := ask(*chainID, p, control.Hello{KeyID: *keyID, Tickets: strings.Fields(*ticket), WGPubkey: pubOf(k)})
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
	_ = rememberClose(closeTarget{ChainID: *chainID, NodeID: p.id, NodeKey: p.key, Endpoint: p.endpoint.String(), WGPubkey: pubOf(k)})
	if reply.DNS != "" {
		if err := setResolver(reply.DNS); err != nil {
			return err
		}
	}
	fmt.Printf("connected through node %d; tunnel address %s; session ends %s\n", p.id, addr, clock(time.Unix(reply.Expires, 0)))
	return nil
}

func down() error {
	if err := needRoot(); err != nil {
		return err
	}
	if err := teardown(); err != nil {
		return err
	}
	fmt.Println("disconnected")
	return nil
}
