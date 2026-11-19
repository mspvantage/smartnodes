// Package dns is the name server an exit node offers its clients.
//
// It passes each query, unread, to the exit machine's own resolver and the
// answer back. It keeps no cache and writes nothing: no log line, no count
// per client, no record of any name asked for. It listens only on the node's
// tunnel address, so only clients inside the tunnel can reach it.
package dns

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	// maxUDP is the largest DNS message over UDP with EDNS.
	maxUDP = 4096
	// Timeout bounds one query.
	Timeout = 5 * time.Second
	// maxInFlight bounds queries handled at once.
	maxInFlight = 256
)

// Forwarder relays DNS queries to an upstream resolver.
type Forwarder struct {
	Listen   netip.Addr
	Upstream netip.AddrPort
}

// Upstream reads the first name server from resolv.conf text.
func Upstream(resolvConf string) (netip.AddrPort, error) {
	for _, line := range strings.Split(resolvConf, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nameserver" {
			if a, err := netip.ParseAddr(f[1]); err == nil {
				return netip.AddrPortFrom(a.Unmap(), 53), nil
			}
		}
	}
	return netip.AddrPort{}, errors.New("dns: no name server in resolv.conf")
}

// Serve answers queries until ctx ends. It returns once both listeners are
// open, so a caller knows the server is ready.
func (f Forwarder) Serve(ctx context.Context) error {
	addr := netip.AddrPortFrom(f.Listen, 53).String()
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		pc.Close()
		return err
	}
	go func() {
		<-ctx.Done()
		pc.Close()
		ln.Close()
	}()
	slots := make(chan struct{}, maxInFlight)
	go f.serveUDP(pc, slots)
	go f.serveTCP(ln, slots)
	return nil
}

func (f Forwarder) serveUDP(pc net.PacketConn, slots chan struct{}) {
	var mu sync.Mutex // guards writes to pc
	for {
		buf := make([]byte, maxUDP)
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			continue // busy: the client will retry
		}
		go func() {
			defer func() { <-slots }()
			answer, err := f.ask(buf[:n])
			if err != nil {
				return
			}
			mu.Lock()
			_, _ = pc.WriteTo(answer, from)
			mu.Unlock()
		}()
	}
}

// ask sends one query upstream from a fresh socket and returns the answer.
func (f Forwarder) ask(query []byte) ([]byte, error) {
	if len(query) < 12 {
		return nil, errors.New("dns: short query")
	}
	c, err := net.DialTimeout("udp", f.Upstream.String(), Timeout)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(Timeout))
	if _, err := c.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, maxUDP)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return nil, err
		}
		// Only an answer to this query, by its id.
		if n >= 12 && buf[0] == query[0] && buf[1] == query[1] {
			return buf[:n], nil
		}
	}
}

func (f Forwarder) serveTCP(ln net.Listener, slots chan struct{}) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		go func() {
			defer func() { <-slots }()
			defer conn.Close()
			up, err := net.DialTimeout("tcp", f.Upstream.String(), Timeout)
			if err != nil {
				return
			}
			defer up.Close()
			deadline := time.Now().Add(4 * Timeout)
			_ = conn.SetDeadline(deadline)
			_ = up.SetDeadline(deadline)
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(up, conn); done <- struct{}{} }()
			go func() { _, _ = io.Copy(conn, up); done <- struct{}{} }()
			<-done
		}()
	}
}
