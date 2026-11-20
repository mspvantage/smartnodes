package chain

import (
	"context"
	"fmt"
	"strconv"
)

// RequestBridge sends a client's paid request for a bridge line.
func (c *CLI) RequestBridge(ctx context.Context, keyID uint64, ticket, requestKey string) error {
	return c.Send(ctx, "tickets", "request-bridge", strconv.FormatUint(keyID, 10), ticket, requestKey)
}

type bridgeRequestJSON struct {
	ID          number `json:"id"`
	RequestKey  string `json:"request_key"`
	RequestKeyC string `json:"requestKey"`
	BridgeID    number `json:"bridge_id"`
	BridgeIDC   number `json:"bridgeId"`
	Sealed      string `json:"sealed"`
	State       number `json:"state"`
}

// BridgeAnswer reads a bridge request's state and sealed answer.
func (c *CLI) BridgeAnswer(ctx context.Context, requestKey string) (string, uint32, error) {
	var res struct {
		Request bridgeRequestJSON `json:"request"`
	}
	if err := c.query(ctx, &res, "tickets", "bridge-request-by-key", requestKey); err != nil {
		return "", 0, err
	}
	return res.Request.Sealed, uint32(res.Request.State), nil
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
