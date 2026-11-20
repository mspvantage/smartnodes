package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mspvantage/smartnodes/vpn/control"
	"github.com/mspvantage/smartnodes/vpn/wallet"
)

// reportBridge reports a saved bridge line dead, through an exit this
// device is connected through, and drops it from the file. Only the device
// that was given the line can report it, with the secret saved beside it;
// the report names neither the device nor any account of its. Once enough
// of those given a line report it, the line is no longer handed out, until
// the bridge moves.
func reportBridge(args []string) error {
	fs := flag.NewFlagSet("report-bridge", flag.ExitOnError)
	file := fs.String("bridges", "/var/lib/smartvpn/bridges.txt", "file of saved bridge lines")
	bridgeArg := fs.Int64("bridge", -1, "the bridge whose line stopped working (node ids start at 0)")
	viaID := fs.Uint64("exit-id", 0, "the exit node this device is connected through")
	viaKey := fs.String("exit-key", "", "that exit node's public key from the registry")
	viaEndpoint := fs.String("exit-endpoint", "", "that exit node's endpoint from the registry")
	chainID := fs.String("chain-id", "smartnodes", "chain id")
	_ = fs.Parse(args)

	if err := needRoot(); err != nil {
		return err
	}
	if *bridgeArg < 0 || *viaKey == "" || *viaEndpoint == "" {
		return errors.New("-bridge, -exit-key and -exit-endpoint are required")
	}
	bridgeID := uint64(*bridgeArg)
	via, err := newPeer(*viaID, *viaKey, *viaEndpoint)
	if err != nil {
		return err
	}
	if err := throughTunnel(via); err != nil {
		return err
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	var keep []string
	reported := false
	for i := 0; i < len(lines); i++ {
		id, rk, secret, ok := parseReport(lines[i])
		if !ok || id != bridgeID || reported {
			keep = append(keep, lines[i])
			continue
		}
		if _, err := ask(*chainID, via, control.Hello{Op: control.OpBridgeReport, RequestKey: rk, Secret: secret, Dead: true}); err != nil {
			return err
		}
		reported = true
		i++ // drop the line that follows its report details
	}
	if !reported {
		return fmt.Errorf("no saved line for bridge %d with report details", bridgeID)
	}
	tmp := *file + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(keep, "\n")), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, *file); err != nil {
		return err
	}
	fmt.Printf("reported bridge %d's line dead and removed it from %s\n", bridgeID, *file)
	return nil
}

func parseReport(l string) (id uint64, requestKey, secret string, ok bool) {
	if !strings.HasPrefix(l, reportPrefix) {
		return 0, "", "", false
	}
	f := strings.Fields(strings.TrimPrefix(l, reportPrefix))
	if len(f) != 3 {
		return 0, "", "", false
	}
	id, err := strconv.ParseUint(f[0], 10, 64)
	if err != nil {
		return 0, "", "", false
	}
	return id, f[1], f[2], true
}

// flagNode is a customer's flag on a node after a bad session. It spends
// one ticket made out to that node, which pays the node as usual, and has
// the node probed more often for a while. It is sent through an exit this
// device is connected through, so the flag names no account of the
// device's. Honest node software passes on a flag against itself too; if
// an exit refuses one, send it through the next exit.
func flagNode(args []string) error {
	fs := flag.NewFlagSet("flag", flag.ExitOnError)
	walletPath := fs.String("wallet", "", "ticket wallet that pays for the flag")
	nodeArg := fs.Int64("node", -1, "the node to flag (node ids start at 0)")
	viaID := fs.Uint64("exit-id", 0, "the exit node this device is connected through")
	viaKey := fs.String("exit-key", "", "that exit node's public key from the registry")
	viaEndpoint := fs.String("exit-endpoint", "", "that exit node's endpoint from the registry")
	_ = fs.Parse(args)

	if err := needRoot(); err != nil {
		return err
	}
	if *walletPath == "" || *nodeArg < 0 || *viaKey == "" || *viaEndpoint == "" {
		return errors.New("-wallet, -node, -exit-key and -exit-endpoint are required")
	}
	nodeID := uint64(*nodeArg)
	via, err := newPeer(*viaID, *viaKey, *viaEndpoint)
	if err != nil {
		return err
	}
	if err := throughTunnel(via); err != nil {
		return err
	}
	w, err := wallet.Open(*walletPath)
	if err != nil {
		return err
	}
	ticket, err := w.Spend(nodeID)
	if err != nil {
		return err
	}
	if _, err := ask(w.ChainID(), via, control.Hello{Op: control.OpFlag, NodeID: nodeID, KeyID: w.KeyID(), Tickets: []string{ticket}}); err != nil {
		return fmt.Errorf("%w (the ticket is spent from the wallet; if the chain did not take it, it can still pay node %d)", err, nodeID)
	}
	fmt.Printf("flagged node %d: it will be probed more often for a while\n", nodeID)
	return nil
}
