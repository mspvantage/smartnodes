package probe

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
)

// Report is one result, kept from when it is decided until it is opened.
type Report struct {
	Round  uint64 `json:"round"`
	NodeID uint64 `json:"node_id"`
	Result uint64 `json:"result"`
	Speed  uint64 `json:"speed_band"`
	// Salt hides the result inside the seal until it is opened.
	Salt string `json:"salt"`
	// Committed is set once the sealed form is on the chain.
	Committed bool `json:"committed"`
}

// NewSalt makes a random 32-byte salt in lowercase hex.
func NewSalt() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Store keeps reports in a file readable by its owner only. It is written
// before every chain step, so a restart never loses a salt for a report
// already sealed on the chain, which would leave it unopenable.
type Store struct {
	path string
	mu   sync.Mutex
	list []Report
}

// OpenStore reads the file, or starts empty if there is none.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.list); err != nil {
		return nil, fmt.Errorf("probe: unreadable %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) save() error {
	raw, _ := json.MarshalIndent(s.list, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Add keeps new reports, replacing any for the same round and node.
func (s *Store) Add(rs ...Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rs {
		kept := s.list[:0]
		for _, x := range s.list {
			if x.Round != r.Round || x.NodeID != r.NodeID {
				kept = append(kept, x)
			}
		}
		s.list = append(kept, r)
	}
	return s.save()
}

// MarkCommitted records that a round's reports are sealed on the chain.
func (s *Store) MarkCommitted(round uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].Round == round {
			s.list[i].Committed = true
		}
	}
	return s.save()
}

// Remove forgets one report, once it is opened.
func (s *Store) Remove(round, nodeID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.list[:0]
	for _, x := range s.list {
		if x.Round != round || x.NodeID != nodeID {
			kept = append(kept, x)
		}
	}
	s.list = kept
	return s.save()
}

// ToOpen returns the committed reports that can be opened in the given
// round, which are those from the round before. It forgets everything
// older, and anything never committed, since none of it can be used.
func (s *Store) ToOpen(round uint64) ([]Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out, kept []Report
	for _, x := range s.list {
		switch {
		case x.Round+1 == round && x.Committed:
			out = append(out, x)
			kept = append(kept, x)
		case x.Round >= round:
			kept = append(kept, x)
		}
	}
	s.list = kept
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, s.save()
}

// Uncommitted returns a round's reports not yet sealed on the chain.
func (s *Store) Uncommitted(round uint64) []Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Report
	for _, x := range s.list {
		if x.Round == round && !x.Committed {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}
