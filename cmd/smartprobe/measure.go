package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mspvantage/smartnodes/vpn/probe"
)

// knownList is a repeatable flag of URL=SHA256 pairs.
type knownList []probe.Known

func (k *knownList) String() string { return fmt.Sprint(*k) }
func (k *knownList) Set(v string) error {
	url, sum, ok := strings.Cut(v, "=")
	if !ok || url == "" || len(sum) != 64 {
		return fmt.Errorf("want URL=SHA256, got %q", v)
	}
	*k = append(*k, probe.Known{URL: url, SHA256: sum})
	return nil
}

// measureCmd runs inside a probe's network space, where the only way out is
// through the two tunnels, and prints what it measured as JSON.
func measureCmd(args []string) error {
	fs := flag.NewFlagSet("measure", flag.ExitOnError)
	speedURL := fs.String("speed-url", defaultSpeedURL, "file to download to measure speed")
	speedTime := fs.Duration("speed-time", 10*time.Second, "longest time to spend measuring speed")
	var known knownList
	fs.Var(&known, "known", "a file and its correct SHA-256, as URL=SHA256; repeatable")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	m := probe.MeasureThrough(ctx, probe.DefaultTools(), *speedURL, *speedTime, known, probe.Fixed)
	return json.NewEncoder(os.Stdout).Encode(m)
}
