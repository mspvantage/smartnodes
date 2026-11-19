// Package nodisk keeps a node's secrets and session state out of files.
//
// Harden applies what the program can do for itself: no core dumps, and its
// memory locked so it is never written to swap. Check reports what only the
// machine's owner can fix, such as system logs kept on disk.
package nodisk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	prSetDumpable = 4
	mclCurrent    = 1
	mclFuture     = 2
	// mclOnFault locks pages only once they are used. Without it, locking
	// would fill every address range the Go runtime reserves with real
	// memory, gigabytes that the program never touches.
	mclOnFault = 4
)

// Harden turns off core dumps for this process and locks its memory. It
// returns a line for anything it could not do.
func Harden() []string {
	var problems []string
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}); err != nil {
		problems = append(problems, fmt.Sprintf("could not turn off core dumps: %v", err))
	}
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); errno != 0 {
		problems = append(problems, fmt.Sprintf("could not mark the process undumpable: %v", errno))
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_MLOCKALL, mclCurrent|mclFuture|mclOnFault, 0, 0); errno != 0 {
		problems = append(problems, fmt.Sprintf("could not lock memory, so it may be swapped to disk: %v", errno))
	}
	return problems
}

// FS reads the machine's settings. Tests replace it.
type FS interface {
	ReadFile(path string) ([]byte, error)
	Glob(pattern string) ([]string, error)
}

// OS reads the real machine.
type OS struct{}

func (OS) ReadFile(p string) ([]byte, error)     { return os.ReadFile(p) }
func (OS) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }

// Check returns a warning for each machine setting that could put session
// details on disk.
func Check(fs FS) []string {
	var warn []string
	if swaps, err := fs.ReadFile("/proc/swaps"); err == nil {
		for _, line := range strings.Split(string(swaps), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			// Swap on an encrypted device is fine.
			if !strings.HasPrefix(f[0], "/dev/dm-") && !strings.HasPrefix(f[0], "/dev/mapper/") && !strings.HasPrefix(f[0], "/dev/zram") {
				warn = append(warn, fmt.Sprintf("swap on %s is not encrypted: turn it off or encrypt it", f[0]))
			}
		}
	}
	if storage := journalStorage(fs); storage != "volatile" && storage != "none" {
		warn = append(warn, fmt.Sprintf("system logs are kept on disk (journald Storage=%s): set Storage=volatile", storage))
	}
	return warn
}

// journalStorage reads journald's Storage setting, drop-ins last, as
// journald does. With nothing set it is "auto", which keeps logs on disk
// when /var/log/journal exists.
func journalStorage(fs FS) string {
	storage := "auto"
	files := []string{"/etc/systemd/journald.conf"}
	for _, dir := range []string{"/usr/lib/systemd/journald.conf.d", "/etc/systemd/journald.conf.d", "/run/systemd/journald.conf.d"} {
		m, _ := fs.Glob(dir + "/*.conf")
		files = append(files, m...)
	}
	for _, f := range files {
		raw, err := fs.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if v, ok := strings.CutPrefix(line, "Storage="); ok {
				storage = strings.TrimSpace(v)
			}
		}
	}
	return storage
}
