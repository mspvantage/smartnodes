package main

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// stateFile remembers what connecting changed, so down can put it back. It
// holds no keys and nothing about the session.
var stateFile = "/run/smartvpn/state.json"

type state struct {
	// Default is the default route before connecting, as "ip route" prints it.
	Default string `json:"default"`
	// Pinned is the route added to keep reaching the entry node directly.
	Pinned string `json:"pinned,omitempty"`
	// Resolv is the name server file before connecting, if it was changed.
	Resolv *string `json:"resolv,omitempty"`
}

// resolvPath is the machine's name server file.
var resolvPath = "/etc/resolv.conf"

// output runs a command and returns what it printed. Tests replace it.
var output = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// run runs a command. Tests replace it.
var run = func(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// routeVia reads how the machine reaches addr now: the gateway (if any) and
// the interface.
func routeVia(addr netip.Addr) (via, dev string, err error) {
	out, err := output("ip", "-4", "-o", "route", "get", addr.String())
	if err != nil {
		return "", "", fmt.Errorf("no route to %s", addr)
	}
	return parseRouteGet(out)
}

func parseRouteGet(out string) (via, dev string, err error) {
	f := strings.Fields(out)
	for i := 0; i+1 < len(f); i++ {
		switch f[i] {
		case "via":
			via = f[i+1]
		case "dev":
			dev = f[i+1]
		}
	}
	if dev == "" {
		return "", "", fmt.Errorf("cannot read route %q", strings.TrimSpace(out))
	}
	if via != "" {
		if _, err := netip.ParseAddr(via); err != nil {
			return "", "", fmt.Errorf("cannot read route %q", strings.TrimSpace(out))
		}
	}
	return via, dev, nil
}

// prepareRoutes saves the default route and pins a direct route to the
// entry node, so the tunnel can become the default route without cutting
// off the entry node itself.
func prepareRoutes(entry netip.Addr) error {
	if _, err := os.Stat(stateFile); err == nil {
		return fmt.Errorf("already connected; run 'smartvpn down' first")
	}
	def, err := output("ip", "-4", "route", "show", "default")
	if err != nil {
		return err
	}
	via, dev, err := routeVia(entry)
	if err != nil {
		return err
	}
	if strings.HasPrefix(dev, "sv") {
		return fmt.Errorf("the entry node is reached through %s; run 'smartvpn down' first", dev)
	}
	pinned := entry.String() + "/32 dev " + dev
	if via != "" {
		pinned = entry.String() + "/32 via " + via + " dev " + dev
	}
	if err := run("ip", append([]string{"route", "replace"}, strings.Fields(pinned)...)...); err != nil {
		return err
	}
	st := state{Default: firstLine(def), Pinned: pinned}
	if err := writeState(st); err != nil {
		_ = run("ip", append([]string{"route", "del"}, strings.Fields(pinned)...)...)
		return err
	}
	return nil
}

// restoreRoutes puts back what prepareRoutes changed.
func restoreRoutes() error {
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		return nil
	}
	var st state
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("unreadable %s: %w", stateFile, err)
	}
	var errs []string
	if st.Resolv != nil {
		if err := writeInPlace(resolvPath, *st.Resolv); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if st.Default != "" {
		if err := run("ip", append([]string{"route", "replace"}, strings.Fields(st.Default)...)...); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if st.Pinned != "" {
		_ = run("ip", append([]string{"route", "del"}, strings.Fields(st.Pinned)...)...)
	}
	if len(errs) > 0 {
		return fmt.Errorf("restoring routes: %s", strings.Join(errs, "; "))
	}
	return os.Remove(stateFile)
}

// setResolver points the machine's name lookups at a name server inside the
// tunnel, keeping the old file to put back on disconnect.
func setResolver(server string) error {
	addr, err := netip.ParseAddr(server)
	if err != nil || !addr.Is4() {
		return fmt.Errorf("the node offered an unusable name server %q", server)
	}
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		return err
	}
	var st state
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	if st.Resolv == nil {
		old, err := os.ReadFile(resolvPath)
		if err != nil {
			return err
		}
		s := string(old)
		st.Resolv = &s
		if err := writeState(st); err != nil {
			return err
		}
	}
	return writeInPlace(resolvPath, "# set by smartvpn; put back by 'smartvpn down'\nnameserver "+addr.String()+"\n")
}

// writeInPlace rewrites a file without replacing it, which keeps a file
// that is mounted over, as a network space's resolv.conf is.
func writeInPlace(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func writeState(st state) error {
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o700); err != nil {
		return err
	}
	raw, _ := json.Marshal(st)
	return os.WriteFile(stateFile, raw, 0o600)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
