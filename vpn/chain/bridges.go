package chain

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mspvantage/smartnodes/vpn/node"
)

// RequestBridge sends a client's paid request for a bridge line.
func (c *CLI) RequestBridge(ctx context.Context, keyID uint64, ticket, requestKey, reportHash string) error {
	args := []string{"tickets", "request-bridge", strconv.FormatUint(keyID, 10), ticket, requestKey}
	if reportHash != "" {
		args = append(args, "--report-hash="+reportHash)
	}
	return c.Send(ctx, args...)
}

type bridgeRequestJSON struct {
	ID          number `json:"id"`
	RequestKey  string `json:"request_key"`
	RequestKeyC string `json:"requestKey"`
	BridgeID    number `json:"bridge_id"`
	BridgeIDC   number `json:"bridgeId"`
	Sealed      string `json:"sealed"`
	State       number `json:"state"`
	Commitment  string `json:"commitment"`
}

// BridgeAnswer reads a bridge request's state and sealed answer.
func (c *CLI) BridgeAnswer(ctx context.Context, requestKey string) (node.BridgeAnswer, error) {
	var res struct {
		Request bridgeRequestJSON `json:"request"`
	}
	if err := c.query(ctx, &res, "tickets", "bridge-request-by-key", requestKey); err != nil {
		return node.BridgeAnswer{}, err
	}
	r := res.Request
	bridgeID := r.BridgeID
	if bridgeID == 0 {
		bridgeID = r.BridgeIDC
	}
	return node.BridgeAnswer{ID: uint64(r.ID), Sealed: r.Sealed, State: uint32(r.State), Commitment: r.Commitment, BridgeID: uint64(bridgeID)}, nil
}

// ReportBridge proves a rubbish answer (requestSecret and newRequestKey) or
// reports a dead line (reportSecret). It returns the bridge asked next
// after a proven rubbish answer, if any; the chain says which in its
// events, read here from the request.
func (c *CLI) ReportBridge(ctx context.Context, requestID uint64, requestSecret, reportSecret, newRequestKey string) (uint64, error) {
	args := []string{"tickets", "report-bridge", strconv.FormatUint(requestID, 10)}
	if requestSecret != "" {
		args = append(args, "--request-secret="+requestSecret, "--new-request-key="+newRequestKey)
	}
	if reportSecret != "" {
		args = append(args, "--report-secret="+reportSecret)
	}
	if err := c.Send(ctx, args...); err != nil {
		return 0, err
	}
	if newRequestKey == "" {
		return 0, nil
	}
	a, err := c.BridgeAnswer(ctx, newRequestKey)
	if err != nil || a.State != 1 {
		return 0, nil
	}
	return a.BridgeID, nil
}

// FlagNode sends a customer's flag on a node, backed by a ticket made out
// to it.
func (c *CLI) FlagNode(ctx context.Context, nodeID, keyID uint64, ticket string) error {
	return c.Send(ctx, "verify", "flag-node", strconv.FormatUint(nodeID, 10), strconv.FormatUint(keyID, 10), ticket)
}

// BridgeFingerprint reads a bridge's fingerprint from the registry.
func (c *CLI) BridgeFingerprint(ctx context.Context, nodeID uint64) (string, error) {
	n, err := c.Node(ctx, nodeID)
	if err != nil {
		return "", err
	}
	if n.Commitment == "" {
		return "", fmt.Errorf("chain: node %d is not a bridge", nodeID)
	}
	return n.Commitment, nil
}

// OpenBridgeRequest is a request a bridge must answer.
type OpenBridgeRequest struct {
	ID         uint64
	RequestKey string
}

// BridgeRequestsFor lists the open requests a bridge must answer.
func (c *CLI) BridgeRequestsFor(ctx context.Context, bridgeID uint64) ([]OpenBridgeRequest, error) {
	var res struct {
		Requests []bridgeRequestJSON `json:"requests"`
	}
	if err := c.query(ctx, &res, "tickets", "bridge-requests-for", strconv.FormatUint(bridgeID, 10)); err != nil {
		return nil, err
	}
	var out []OpenBridgeRequest
	for _, r := range res.Requests {
		out = append(out, OpenBridgeRequest{ID: uint64(r.ID), RequestKey: either(r.RequestKey, r.RequestKeyC)})
	}
	return out, nil
}

// AnswerBridge sends a bridge's sealed answer.
func (c *CLI) AnswerBridge(ctx context.Context, requestID uint64, sealed string) error {
	return c.Send(ctx, "tickets", "answer-bridge", strconv.FormatUint(requestID, 10), sealed)
}

// BridgeLineState is how a bridge's current line stands on the chain.
type BridgeLineState struct {
	Given, Dead   uint64
	Stopped, Full bool
}

// BridgeLine reads how a bridge's current line stands.
func (c *CLI) BridgeLine(ctx context.Context, bridgeID uint64) (BridgeLineState, error) {
	var res struct {
		Line struct {
			Count number `json:"count"`
			Dead  number `json:"dead"`
		} `json:"line"`
		Stopped bool `json:"stopped"`
		Full    bool `json:"full"`
	}
	if err := c.query(ctx, &res, "tickets", "bridge-line", strconv.FormatUint(bridgeID, 10)); err != nil {
		return BridgeLineState{}, err
	}
	return BridgeLineState{Given: uint64(res.Line.Count), Dead: uint64(res.Line.Dead), Stopped: res.Stopped, Full: res.Full}, nil
}
