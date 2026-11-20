// Command smartprobe is the SmartNodes prober: a bonded account that tests
// the nodes the chain assigns to it, as an ordinary paying customer, and
// reports what it finds.
//
// Each round it asks the chain which nodes it must probe. For each one it
// builds a fresh, separate network space on this machine, connects through
// two hops with smartvpn exactly as a customer would, paying with tickets
// from its wallet, and measures speed, whether known files come back
// unaltered, and whether name lookups are answered truthfully. It seals its
// results on the chain before the round ends and opens them in the next
// round, at random times.
//
// It must run as root, to create network spaces and WireGuard interfaces.
//
//	smartprobe run -from prober1 -home ~/.smartnodes -wallet prober1-wallet.json
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = run(os.Args[2:])
	case "measure":
		err = measureCmd(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "smartprobe:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  smartprobe run -from KEY -home DIR -wallet FILE [-rpc URL] [-chain-id ID] [-state-dir DIR] ...
  smartprobe measure ...   (used by run, inside a probe's network space)`)
	os.Exit(2)
}
