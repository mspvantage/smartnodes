package nodisk

import (
	"errors"
	"path"
	"strings"
	"testing"
)

type fakeFS map[string]string

func (f fakeFS) ReadFile(p string) ([]byte, error) {
	if s, ok := f[p]; ok {
		return []byte(s), nil
	}
	return nil, errors.New("no such file")
}

func (f fakeFS) Glob(pattern string) ([]string, error) {
	var out []string
	for p := range f {
		if ok, _ := path.Match(pattern, p); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

const swapHeader = "Filename\tType\tSize\tUsed\tPriority\n"

func TestCheck(t *testing.T) {
	bad := fakeFS{
		"/proc/swaps":                swapHeader + "/swap.img file 2097148 0 -2\n",
		"/etc/systemd/journald.conf": "[Journal]\n#Storage=auto\n",
	}
	w := strings.Join(Check(bad), "\n")
	if !strings.Contains(w, "swap on /swap.img") || !strings.Contains(w, "Storage=auto") {
		t.Fatalf("warnings:\n%s", w)
	}

	good := fakeFS{
		"/proc/swaps":                                 swapHeader + "/dev/dm-1 partition 1000 0 -2\n",
		"/etc/systemd/journald.conf":                  "[Journal]\nStorage=persistent\n",
		"/etc/systemd/journald.conf.d/smartnode.conf": "[Journal]\nStorage=volatile\n",
	}
	if w := Check(good); len(w) != 0 {
		t.Fatalf("warnings on a good machine: %v", w)
	}

	noSwap := fakeFS{"/proc/swaps": swapHeader, "/etc/systemd/journald.conf": "Storage=none\n"}
	if w := Check(noSwap); len(w) != 0 {
		t.Fatalf("%v", w)
	}
}
