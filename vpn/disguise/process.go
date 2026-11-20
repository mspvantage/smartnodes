package disguise

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// WriteConfig saves a configuration readable by its owner only: the node's
// holds its private key.
func WriteConfig(dir, name string, cfg []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, cfg, 0o600); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

// Process is a running Xray.
type Process struct {
	cmd  *exec.Cmd
	done chan error
}

// Start runs Xray with a configuration file. Its output is discarded:
// nothing it might print about connections is kept.
func Start(xray, config string) (*Process, error) {
	cmd := exec.Command(xray, "run", "-c", config)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("disguise: starting xray: %w", err)
	}
	p := &Process{cmd: cmd, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	return p, nil
}

// Done is closed with Xray's exit.
func (p *Process) Done() <-chan error { return p.done }

// Stop ends Xray.
func (p *Process) Stop() {
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
	}
}

// StartDetached runs Xray in its own session, so it keeps running after the
// command that started it returns, and gives its process id.
func StartDetached(xray, config string) (int, error) {
	cmd := exec.Command(xray, "run", "-c", config)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("disguise: starting xray: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

// StopDetached ends an Xray started with StartDetached. It checks the
// process is Xray first, so a reused process id is never signalled.
func StopDetached(pid int, xray string) {
	if pid <= 0 {
		return
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return
	}
	want, _ := filepath.EvalSymlinks(xray)
	if want == "" {
		want = xray
	}
	if exe != want && filepath.Base(exe) != filepath.Base(want) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
}

// WaitListening waits until something accepts TCP connections at addr.
func WaitListening(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("disguise: xray did not start listening at " + addr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
