// Command smartvpn is the stage 1 SmartNodes client. It pays one node with a
// ticket and routes all of this machine's traffic through a WireGuard tunnel
// to it.
//
// It must run as root, because it creates a WireGuard interface and changes
// the default route.
//
//	smartvpn up -node-id 0 -node-key KEY -endpoint 203.0.113.5:51820 -key-id 0 -ticket TICKET
//	smartvpn down
package main

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/mspvantage/smartnodes/vpn/control"
	"github.com/mspvantage/smartnodes/vpn/wg"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "up":
		err = up(os.Args[2:])
	case "down":
		err = down(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "smartvpn:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: smartvpn up -node-id N -node-key KEY -endpoint HOST:PORT -key-id K -ticket TICKET [-chain-id ID] [-iface NAME]")
	fmt.Fprintln(os.Stderr, "       smartvpn down [-iface NAME]")
	os.Exit(2)
}

func up(args []string) error {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	nodeID := fs.Uint64("node-id", 0, "id of the node to use")
	nodeKey := fs.String("node-key", "", "the node's public key from the registry")
	endpoint := fs.String("endpoint", "", "the node's endpoint from the registry")
	chainID := fs.String("chain-id", "smartnodes", "chain id")
	keyID := fs.Uint64("key-id", 0, "issuer key the ticket was signed under")
	ticket := fs.String("ticket", "", "a ticket spent to this node, from 'ticketctl wallet-spend'")
	iface := fs.String("iface", "sv0", "WireGuard interface to create")
	_ = fs.Parse(args)

	if os.Geteuid() != 0 {
		return errors.New("must run as root, to create the WireGuard interface")
	}
	if *nodeKey == "" || *endpoint == "" || *ticket == "" {
		return errors.New("-node-key, -endpoint and -ticket are required")
	}
	nodePub, err := control.DecodePublicKey(*nodeKey)
	if err != nil {
		return err
	}
	udp, err := net.ResolveUDPAddr("udp4", *endpoint)
	if err != nil {
		return fmt.Errorf("endpoint: %w", err)
	}
	ap := udp.AddrPort()
	ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())

	// A WireGuard key for this session only.
	session, err := control.NewKey()
	if err != nil {
		return err
	}

	conn, err := net.DialTimeout("tcp", ap.String(), control.Timeout)
	if err != nil {
		return fmt.Errorf("cannot reach the node: %w", err)
	}
	reply, err := control.Dial(conn, *chainID, *nodeID, nodePub, control.Hello{
		KeyID:    *keyID,
		Tickets:  strings.Fields(*ticket),
		WGPubkey: base64.StdEncoding.EncodeToString(session.Public),
	})
	conn.Close()
	if err != nil {
		return err
	}
	if !reply.OK {
		return fmt.Errorf("the node refused: %s", reply.Error)
	}
	addr, err := netip.ParseAddr(reply.TunnelIP)
	if err != nil || !addr.Is4() {
		return fmt.Errorf("the node gave an unusable tunnel address %q", reply.TunnelIP)
	}

	tunnel := wg.Interface{Name: *iface, Run: wg.Exec{}}
	if err := tunnel.SetUpClient(session.Private, *nodeKey, ap, addr); err != nil {
		return err
	}
	fmt.Printf("connected through node %d; tunnel address %s; session ends %s\n",
		*nodeID, addr, time.Unix(reply.Expires, 0).Format("15:04:05"))
	return nil
}

func down(args []string) error {
	fs := flag.NewFlagSet("down", flag.ExitOnError)
	iface := fs.String("iface", "sv0", "WireGuard interface to remove")
	_ = fs.Parse(args)
	if err := (wg.Interface{Name: *iface, Run: wg.Exec{}}).Delete(); err != nil {
		return err
	}
	fmt.Println("disconnected")
	return nil
}
