package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mspvantage/smartnodes/vpn/chain"
	"github.com/mspvantage/smartnodes/vpn/disguise"
)

// realityOpts are an entry node's disguise settings.
type realityOpts struct {
	port       uint
	cover      string
	serverName string
	xray       string
	dir        string
}

// loadKeys reads the disguise keys, making them the first time. Keeping
// them means what the node published stays valid across restarts.
func loadKeys(path string) (disguise.ServerKeys, error) {
	var k disguise.ServerKeys
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if k, err = disguise.NewServerKeys(); err != nil {
			return k, err
		}
		raw, _ = json.MarshalIndent(k, "", "  ")
		if _, err := disguise.WriteConfig(filepath.Dir(path), filepath.Base(path), raw); err != nil {
			return k, err
		}
		log.Printf("made new disguise keys in %s", path)
		return k, nil
	}
	if err != nil {
		return k, err
	}
	if err := json.Unmarshal(raw, &k); err != nil {
		return k, fmt.Errorf("unreadable %s: %w", path, err)
	}
	return k, k.Check()
}

// startReality runs Xray for as long as ctx lasts, restarting it if it
// stops, and says whether the chain shows the same disguise.
func startReality(ctx context.Context, o realityOpts, listen netip.AddrPort, info chain.NodeInfo, nodeID uint64) error {
	if o.port > 65535 {
		return fmt.Errorf("port %d", o.port)
	}
	host, _, err := net.SplitHostPort(o.cover)
	if err != nil {
		return fmt.Errorf("-reality-cover must be host:port: %w", err)
	}
	if o.serverName == "" {
		o.serverName = host
	}
	xray, err := exec.LookPath(o.xray)
	if err != nil {
		return fmt.Errorf("the Xray program was not found (%s); install it or give -xray", o.xray)
	}
	keys, err := loadKeys(filepath.Join(o.dir, "reality-keys.json"))
	if err != nil {
		return err
	}
	pub := keys.Published(uint16(o.port), o.serverName)
	if err := pub.Validate(); err != nil {
		return err
	}

	// Key holders may reach only this node's own WireGuard and payment port.
	allow := listen
	if listen.Addr().IsUnspecified() {
		allow = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), listen.Port())
	}
	cfg, err := disguise.ServerConfig(keys, netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(o.port)), o.cover, o.serverName, allow)
	if err != nil {
		return err
	}
	path, err := disguise.WriteConfig(o.dir, "xray-server.json", cfg)
	if err != nil {
		return err
	}
	if out, err := exec.Command(xray, "run", "-test", "-c", path).CombinedOutput(); err != nil {
		return fmt.Errorf("Xray rejects its settings: %v: %s", err, lastLines(out))
	}

	if info.Reality == nil || *info.Reality != pub {
		log.Printf("the chain does not show this disguise yet; the operator publishes it with:")
		log.Printf("  smartnodesd tx registry set-reality %d %d %s %s %s %s --from <operator>",
			nodeID, pub.Port, pub.PublicKey, pub.ShortID, pub.ServerName, pub.UUID)
	} else {
		log.Printf("the disguise on the chain matches this node's")
	}

	p, err := disguise.Start(xray, path)
	if err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				p.Stop()
				return
			case <-p.Done():
				log.Printf("Xray stopped; restarting it in 5 seconds")
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
				if p, err = disguise.Start(xray, path); err != nil {
					log.Printf("could not restart Xray: %v", err)
					return
				}
			}
		}
	}()
	log.Printf("disguised devices accepted on TCP port %d, looking like %s", o.port, o.serverName)
	return nil
}

func lastLines(out []byte) string {
	if len(out) > 400 {
		out = out[len(out)-400:]
	}
	return string(out)
}
