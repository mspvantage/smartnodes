// Package chain lets the node and client programs talk to the SmartNodes
// chain by running the chain's own command-line program, smartnodesd.
//
// This keeps the VPN programs small and free of the chain's large code base
// for now. It is a stage 1 choice: later stages can talk to the chain
// directly.
package chain

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mspvantage/smartnodes/vpn/disguise"
	"github.com/mspvantage/smartnodes/vpn/node"
)

// Runner runs smartnodesd with the given arguments and returns what it
// printed. On failure the error carries what it printed to stderr.
type Runner func(ctx context.Context, args []string) ([]byte, error)

// Exec returns a Runner for the program at path.
func Exec(path string) Runner {
	return func(ctx context.Context, args []string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, path, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return stdout.Bytes(), fmt.Errorf("%v: %s %s", err, strings.TrimSpace(stderr.String()), strings.TrimSpace(stdout.String()))
		}
		return stdout.Bytes(), nil
	}
}

// CLI talks to the chain through smartnodesd.
type CLI struct {
	Run Runner
	// RPC is the chain node to talk to, such as tcp://localhost:26657.
	RPC     string
	ChainID string
	// NodeID is the VPN node redemptions are for.
	NodeID uint64
	// From, KeyringBackend and Home pick the key that signs redemptions.
	From           string
	KeyringBackend string
	Home           string
	GasPrices      string
	// PollInterval and PollTimeout control waiting for a transaction.
	PollInterval time.Duration
	PollTimeout  time.Duration
}

var _ node.Chain = (*CLI)(nil)

func (c *CLI) query(ctx context.Context, out any, args ...string) error {
	args = append(append([]string{"query"}, args...), "-o", "json", "--node", c.RPC)
	raw, err := c.Run(ctx, args)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("chain: unreadable answer to %s: %w", strings.Join(args[:3], " "), err)
	}
	return nil
}

// number reads a JSON number that the chain may print as a number, as a
// string, or leave out when it is zero.
type number int64

func (n *number) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*n = number(v)
	return nil
}

// IssuerKey reads an issuer key.
func (c *CLI) IssuerKey(ctx context.Context, id uint64) (node.IssuerKey, error) {
	var res struct {
		IssuerKey struct {
			Pubkey    string `json:"pubkey"`
			RedeemEnd number `json:"redeem_end"`
		} `json:"issuer_key"`
	}
	if err := c.query(ctx, &res, "tickets", "get-issuer-key", strconv.FormatUint(id, 10)); err != nil {
		return node.IssuerKey{}, err
	}
	pk, err := base64.StdEncoding.DecodeString(res.IssuerKey.Pubkey)
	if err != nil || len(pk) == 0 {
		return node.IssuerKey{}, fmt.Errorf("chain: issuer key %d has no readable public key", id)
	}
	return node.IssuerKey{Pubkey: pk, RedeemEnd: int64(res.IssuerKey.RedeemEnd)}, nil
}

// TicketSpent reports whether a ticket has been redeemed.
func (c *CLI) TicketSpent(ctx context.Context, keyID uint64, ticketKey []byte) (bool, error) {
	var res struct {
		Spent bool `json:"spent"`
	}
	err := c.query(ctx, &res, "tickets", "ticket-spent", strconv.FormatUint(keyID, 10), hex.EncodeToString(ticketKey))
	return res.Spent, err
}

// NodeInfo is a node's registry entry.
type NodeInfo struct {
	ID       uint64
	Operator string
	// ServiceKey is the account allowed to redeem for the operator, if any.
	ServiceKey string
	// Reality is the entry node's disguised way in, if it offers one.
	Reality  *disguise.Reality
	Endpoint string
	Pubkey   string
	Tier     uint64
	Status   uint64
}

// Registry tiers and statuses, as the registry module numbers them.
const (
	TierEntry    = 1
	TierExit     = 2
	StatusActive = 1
)

type nodeJSON struct {
	ID       number `json:"id"`
	Operator string `json:"operator"`
	Endpoint string `json:"endpoint"`
	Pubkey   string `json:"pubkey"`
	Tier     number `json:"tier"`
	Status   number `json:"status"`
	Service  string `json:"service_key"`
	// The chain's JSON may also spell it in camel case.
	ServiceCamel string       `json:"serviceKey"`
	Reality      *realityJSON `json:"reality"`
}

type realityJSON struct {
	Port        number `json:"port"`
	PublicKey   string `json:"public_key"`
	PublicKeyC  string `json:"publicKey"`
	ShortID     string `json:"short_id"`
	ShortIDC    string `json:"shortId"`
	ServerName  string `json:"server_name"`
	ServerNameC string `json:"serverName"`
	UUID        string `json:"uuid"`
}

func either(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// reality reads a published disguise; one that does not check out is
// treated as not offered.
func (r *realityJSON) reality() *disguise.Reality {
	if r == nil || r.Port == 0 || r.Port > 65535 {
		return nil
	}
	out := disguise.Reality{Port: uint16(r.Port), PublicKey: either(r.PublicKey, r.PublicKeyC),
		ShortID: either(r.ShortID, r.ShortIDC), ServerName: either(r.ServerName, r.ServerNameC), UUID: r.UUID}
	if out.Validate() != nil {
		return nil
	}
	return &out
}

func (n nodeJSON) info() NodeInfo {
	service := n.Service
	if service == "" {
		service = n.ServiceCamel
	}
	return NodeInfo{ID: uint64(n.ID), Operator: n.Operator, ServiceKey: service, Reality: n.Reality.reality(), Endpoint: n.Endpoint, Pubkey: n.Pubkey, Tier: uint64(n.Tier), Status: uint64(n.Status)}
}

// SignerAddress returns the address of the key that signs redemptions.
func (c *CLI) SignerAddress(ctx context.Context) (string, error) {
	out, err := c.Run(ctx, []string{"keys", "show", c.From, "-a", "--keyring-backend", c.KeyringBackend, "--home", c.Home})
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(string(out))
	if addr == "" || strings.ContainsAny(addr, " \n") {
		return "", fmt.Errorf("chain: unreadable address for key %s", c.From)
	}
	return addr, nil
}

// Node reads a node's registry entry.
func (c *CLI) Node(ctx context.Context, id uint64) (NodeInfo, error) {
	var res struct {
		Node nodeJSON `json:"node"`
	}
	if err := c.query(ctx, &res, "registry", "get-node", strconv.FormatUint(id, 10)); err != nil {
		return NodeInfo{}, err
	}
	info := res.Node.info()
	info.ID = id
	return info, nil
}

// listPage is how many nodes one list query asks for.
const listPage = 1000

// ListNodes reads every node in the registry, a page at a time.
func (c *CLI) ListNodes(ctx context.Context) ([]NodeInfo, error) {
	var all []NodeInfo
	for offset := 0; ; offset += listPage {
		var res struct {
			Node []nodeJSON `json:"node"`
		}
		if err := c.query(ctx, &res, "registry", "list-node",
			"--page-offset", strconv.Itoa(offset), "--page-limit", strconv.Itoa(listPage)); err != nil {
			return nil, err
		}
		for _, n := range res.Node {
			all = append(all, n.info())
		}
		if len(res.Node) < listPage {
			return all, nil
		}
		if offset > 1000*listPage {
			return nil, fmt.Errorf("chain: the registry list does not end")
		}
	}
}

var (
	ticketFault = regexp.MustCompile(`ticket (\d+): ([^\[\n]*)`)
	keyExpired  = "issuer key has passed its redemption deadline"
)

// classify turns the chain's error text into the node's error types.
func classify(text string) error {
	if strings.Contains(text, keyExpired) {
		return node.ErrKeyExpired
	}
	if m := ticketFault.FindStringSubmatch(text); m != nil {
		i, err := strconv.Atoi(m[1])
		if err == nil {
			return &node.RedeemError{Index: i, Reason: strings.TrimSpace(m[2])}
		}
	}
	return nil
}

type txResult struct {
	TxHash string `json:"txhash"`
	Code   number `json:"code"`
	RawLog string `json:"raw_log"`
}

// Redeem redeems tickets for the node in one transaction and waits until the
// transaction is in a block. If it was sent but not seen in a block in time,
// the error wraps node.ErrUnconfirmed: it may still go through.
//
// The command first simulates the transaction to work out its fee, and a
// simulation fails if any ticket is refused, naming it. So a ticket spent
// elsewhere is usually found before anything is sent.
func (c *CLI) Redeem(ctx context.Context, keyID uint64, tickets [][]byte) error {
	if len(tickets) == 0 {
		return nil
	}
	args := []string{"tx", "tickets", "redeem-tickets", strconv.FormatUint(c.NodeID, 10), strconv.FormatUint(keyID, 10)}
	for _, t := range tickets {
		args = append(args, base64.StdEncoding.EncodeToString(t))
	}
	args = append(args,
		"--from", c.From, "--keyring-backend", c.KeyringBackend, "--home", c.Home,
		"--chain-id", c.ChainID, "--node", c.RPC,
		"--gas", "auto", "--gas-adjustment", "1.5", "--gas-prices", c.GasPrices,
		"--yes", "-o", "json")

	raw, err := c.Run(ctx, args)
	if err != nil {
		if known := classify(err.Error()); known != nil {
			return known
		}
		return fmt.Errorf("chain: redeem failed: %w", err)
	}
	var sent txResult
	if err := json.Unmarshal(lastJSON(raw), &sent); err != nil || sent.TxHash == "" {
		return fmt.Errorf("chain: unreadable answer to redeem: %s", strings.TrimSpace(string(raw)))
	}
	if sent.Code != 0 {
		if known := classify(sent.RawLog); known != nil {
			return known
		}
		return fmt.Errorf("chain: redeem refused: %s", sent.RawLog)
	}
	return c.wait(ctx, sent.TxHash)
}

// Send signs and sends a transaction with the CLI's key and waits until it
// is in a block. args are the command after "tx", such as
// "verify", "reveal-report", "3", ...
func (c *CLI) Send(ctx context.Context, args ...string) error {
	full := append([]string{"tx"}, args...)
	full = append(full,
		"--from", c.From, "--keyring-backend", c.KeyringBackend, "--home", c.Home,
		"--chain-id", c.ChainID, "--node", c.RPC,
		"--gas", "auto", "--gas-adjustment", "1.5", "--gas-prices", c.GasPrices,
		"--yes", "-o", "json")
	raw, err := c.Run(ctx, full)
	if err != nil {
		return fmt.Errorf("chain: %s %s: %w", args[0], args[1], err)
	}
	var sent txResult
	if err := json.Unmarshal(lastJSON(raw), &sent); err != nil || sent.TxHash == "" {
		return fmt.Errorf("chain: unreadable answer to %s %s: %s", args[0], args[1], strings.TrimSpace(string(raw)))
	}
	if sent.Code != 0 {
		return fmt.Errorf("chain: %s %s refused: %s", args[0], args[1], sent.RawLog)
	}
	return c.wait(ctx, sent.TxHash)
}

// wait polls until a transaction is in a block and reports its result.
func (c *CLI) wait(ctx context.Context, hash string) error {
	interval, timeout := c.PollInterval, c.PollTimeout
	if interval <= 0 {
		interval = time.Second
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		var res txResult
		err := c.query(ctx, &res, "tx", hash)
		if err == nil {
			if res.Code == 0 {
				return nil
			}
			if known := classify(res.RawLog); known != nil {
				return known
			}
			return fmt.Errorf("chain: transaction failed in block: %s", res.RawLog)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: transaction %s not seen in a block: %v", node.ErrUnconfirmed, hash, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: transaction %s: %v", node.ErrUnconfirmed, hash, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// lastJSON returns the last line of output that looks like a JSON object.
// Some commands print notes, such as the gas estimate, before the result.
func lastJSON(out []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		l := bytes.TrimSpace(lines[i])
		if len(l) > 0 && l[0] == '{' {
			return l
		}
	}
	return out
}
