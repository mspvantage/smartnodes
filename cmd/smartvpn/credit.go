package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// creditFile is an exit credit bought ahead: the session key it is held for
// and the exit node that holds it. Anyone with the file can use the credit,
// so it is written readable by root only and deleted once used.
type creditFile struct {
	ChainID   string `json:"chain_id"`
	ExitID    uint64 `json:"exit_id"`
	ExitKey   string `json:"exit_key"`
	Endpoint  string `json:"endpoint"`
	WGPrivate string `json:"wg_private"`
	Expires   int64  `json:"expires"`
}

func saveCredit(path string, c creditFile) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadCredit(path string, now time.Time) (creditFile, error) {
	var c creditFile
	fi, err := os.Stat(path)
	if err != nil {
		return c, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return c, fmt.Errorf("%s is readable by others; it must be mode 0600", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("unreadable credit file: %w", err)
	}
	if c.ExitKey == "" || c.Endpoint == "" || c.WGPrivate == "" {
		return c, errors.New("incomplete credit file")
	}
	if !now.Before(time.Unix(c.Expires, 0)) {
		return c, errors.New("the credit has lapsed")
	}
	return c, nil
}
