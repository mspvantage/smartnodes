package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// RoundInfo is the current verification round.
type RoundInfo struct {
	Round       uint64
	StartHeight int64
	NextHeight  int64
}

// Round reads the current verification round.
func (c *CLI) Round(ctx context.Context) (RoundInfo, error) {
	var res struct {
		Round  number `json:"round"`
		Start  number `json:"start_height"`
		Next   number `json:"next_round_height"`
		StartC number `json:"startHeight"`
		NextC  number `json:"nextRoundHeight"`
	}
	if err := c.query(ctx, &res, "verify", "round"); err != nil {
		return RoundInfo{}, err
	}
	r := RoundInfo{Round: uint64(res.Round), StartHeight: int64(res.Start), NextHeight: int64(res.Next)}
	if r.StartHeight == 0 {
		r.StartHeight = int64(res.StartC)
	}
	if r.NextHeight == 0 {
		r.NextHeight = int64(res.NextC)
	}
	return r, nil
}

// Height reads the chain's latest block height.
func (c *CLI) Height(ctx context.Context) (int64, error) {
	raw, err := c.Run(ctx, []string{"status", "--node", c.RPC})
	if err != nil {
		return 0, err
	}
	var v any
	if err := json.Unmarshal(lastJSON(raw), &v); err != nil {
		return 0, fmt.Errorf("chain: unreadable status: %w", err)
	}
	if h, ok := findKey(v, "latest_block_height"); ok {
		return h, nil
	}
	return 0, fmt.Errorf("chain: status has no block height")
}

// findKey searches decoded JSON for a key holding a number.
func findKey(v any, key string) (int64, bool) {
	switch t := v.(type) {
	case map[string]any:
		if x, ok := t[key]; ok {
			var n number
			raw, _ := json.Marshal(x)
			if json.Unmarshal(raw, &n) == nil {
				return int64(n), true
			}
		}
		for _, x := range t {
			if n, ok := findKey(x, key); ok {
				return n, true
			}
		}
	case []any:
		for _, x := range t {
			if n, ok := findKey(x, key); ok {
				return n, true
			}
		}
	}
	return 0, false
}

// ProberInfo is a prober's registry entry.
type ProberInfo struct {
	ID     uint64
	Status uint64
}

// ProberStatusActive is the status of a prober in the active set.
const ProberStatusActive = 2

// ProberByOwner finds the prober registered by an account.
func (c *CLI) ProberByOwner(ctx context.Context, owner string) (ProberInfo, error) {
	for offset := 0; offset <= 1000*listPage; offset += listPage {
		var res struct {
			Prober []struct {
				ID     number `json:"id"`
				Owner  string `json:"owner"`
				Status number `json:"status"`
			} `json:"prober"`
		}
		if err := c.query(ctx, &res, "verify", "list-prober",
			"--page-offset", strconv.Itoa(offset), "--page-limit", strconv.Itoa(listPage)); err != nil {
			return ProberInfo{}, err
		}
		for _, p := range res.Prober {
			if p.Owner == owner {
				return ProberInfo{ID: uint64(p.ID), Status: uint64(p.Status)}, nil
			}
		}
		if len(res.Prober) < listPage {
			break
		}
	}
	return ProberInfo{}, fmt.Errorf("chain: %s is not a registered prober", owner)
}

// Assignment says whether a node is probed this round, and by whom.
type Assignment struct {
	Round    uint64
	Selected bool
	Probers  []uint64
}

// Assigned reads a node's assignment for the current round.
func (c *CLI) Assigned(ctx context.Context, nodeID uint64) (Assignment, error) {
	var res struct {
		Round    number   `json:"round"`
		Selected bool     `json:"selected"`
		Probers  []number `json:"prober_ids"`
		ProbersC []number `json:"proberIds"`
	}
	if err := c.query(ctx, &res, "verify", "assignment", strconv.FormatUint(nodeID, 10)); err != nil {
		return Assignment{}, err
	}
	a := Assignment{Round: uint64(res.Round), Selected: res.Selected}
	ids := res.Probers
	if len(ids) == 0 {
		ids = res.ProbersC
	}
	for _, id := range ids {
		a.Probers = append(a.Probers, uint64(id))
	}
	return a, nil
}

// CommitReports posts sealed reports for a round in one transaction.
func (c *CLI) CommitReports(ctx context.Context, round uint64, nodeIDs []uint64, hashes []string) error {
	if len(nodeIDs) == 0 || len(nodeIDs) != len(hashes) {
		return fmt.Errorf("chain: %d nodes and %d hashes", len(nodeIDs), len(hashes))
	}
	ids := make([]string, len(nodeIDs))
	for i, id := range nodeIDs {
		ids[i] = strconv.FormatUint(id, 10)
	}
	args := append([]string{"verify", "commit-reports", strconv.FormatUint(round, 10), strings.Join(ids, ",")}, hashes...)
	return c.Send(ctx, args...)
}

// RevealReport opens one sealed report from the previous round.
func (c *CLI) RevealReport(ctx context.Context, round, nodeID, result, speedBand uint64, salt string) error {
	return c.Send(ctx, "verify", "reveal-report",
		strconv.FormatUint(round, 10), strconv.FormatUint(nodeID, 10),
		strconv.FormatUint(result, 10), strconv.FormatUint(speedBand, 10), salt)
}

// ProberAssignments lists the nodes a prober must probe this round.
func (c *CLI) ProberAssignments(ctx context.Context, proberID uint64) (round uint64, nodeIDs []uint64, err error) {
	var res struct {
		Round    number   `json:"round"`
		NodeIDs  []number `json:"node_ids"`
		NodeIDsC []number `json:"nodeIds"`
	}
	if err := c.query(ctx, &res, "verify", "prober-assignments", strconv.FormatUint(proberID, 10)); err != nil {
		return 0, nil, err
	}
	ids := res.NodeIDs
	if len(ids) == 0 {
		ids = res.NodeIDsC
	}
	for _, id := range ids {
		nodeIDs = append(nodeIDs, uint64(id))
	}
	return uint64(res.Round), nodeIDs, nil
}

// MinSpeeds reads the slowest speeds, in Mbit/s, at which probes of entry
// and exit nodes pass. Zero means no minimum.
func (c *CLI) MinSpeeds(ctx context.Context) (entry, exit uint64, err error) {
	var res struct {
		Params struct {
			Entry  number `json:"min_entry_mbps"`
			Exit   number `json:"min_exit_mbps"`
			EntryC number `json:"minEntryMbps"`
			ExitC  number `json:"minExitMbps"`
		} `json:"params"`
	}
	if err := c.query(ctx, &res, "verify", "params"); err != nil {
		return 0, 0, err
	}
	entry, exit = uint64(res.Params.Entry), uint64(res.Params.Exit)
	if entry == 0 {
		entry = uint64(res.Params.EntryC)
	}
	if exit == 0 {
		exit = uint64(res.Params.ExitC)
	}
	return entry, exit, nil
}
