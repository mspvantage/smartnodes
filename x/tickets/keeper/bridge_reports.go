package keeper

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/vpn/bridge"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// Reports on bridge lines.
//
// A rubbish answer is proven: the device shows its one-time private key,
// the chain opens the sealed answer and checks the line inside against the
// fingerprint the bridge had committed to. A line that does not open, does
// not parse, or has another fingerprint is rubbish. The bridge's bond is
// burned in part and, while its payment is still held, the request passes
// to another bridge, so the device still gets a working line for its
// ticket.
//
// A dead line cannot be proven: a censor can make a line fail for some
// people and not others. So a dead report only counts, and once enough of
// those given a line report it, the line stops being handed out until the
// bridge moves to a new address or port. Nobody already using the line is
// cut off. The report is made with the secret behind the request's
// report_hash, which shows the reporter was given the line without naming
// it.

const (
	EventTypeBridgeBad  = "bridge_bad_answer"
	EventTypeBridgeDead = "bridge_dead_report"
	EventTypeBridgeStop = "bridge_line_stopped"

	// GasPerBridgeProof covers opening and parsing an answer.
	GasPerBridgeProof = 50_000
)

// ReportBridge handles both kinds of report.
func (k msgServer) ReportBridge(ctx context.Context, msg *types.MsgReportBridge) (*types.MsgReportBridgeResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Signer); err != nil {
		return nil, errorsmod.Wrap(err, "invalid signer address")
	}
	if (msg.RequestSecret == "") == (msg.ReportSecret == "") {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "give either the request secret (rubbish answer) or the report secret (dead line)")
	}
	req, err := k.BridgeRequest.Get(ctx, msg.RequestId)
	if err != nil {
		return nil, errorsmod.Wrapf(types.ErrBridgeRequest, "no request %d", msg.RequestId)
	}
	if req.State != types.BridgeAnswered {
		return nil, errorsmod.Wrapf(types.ErrBridgeRequest, "request %d has no answer to report", req.Id)
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if msg.RequestSecret != "" {
		return k.reportBad(ctx, params, req, msg.RequestSecret, msg.NewRequestKey)
	}
	return k.reportDead(ctx, params, req, msg.ReportSecret)
}

// AnswerIsRubbish opens a sealed answer with the request's private key and
// says whether it fails to hold a line with the given fingerprint. It
// returns an error if the key is not the request's, which proves nothing.
func AnswerIsRubbish(requestKey string, secret []byte, sealed string, commitment string) (bool, error) {
	priv, err := ecdh.X25519().NewPrivateKey(secret)
	if err != nil {
		return false, errorsmod.Wrap(types.ErrBridgeRequest, "the request secret is not a key")
	}
	if base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()) != requestKey {
		return false, errorsmod.Wrap(types.ErrBridgeRequest, "the request secret does not match the request key")
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return true, nil
	}
	text, err := bridge.Open(priv, raw)
	if err != nil {
		return true, nil
	}
	line, err := bridge.Parse(text)
	if err != nil {
		return true, nil
	}
	return line.Fingerprint() != commitment, nil
}

func (k msgServer) reportBad(ctx context.Context, params types.Params, req types.BridgeRequest, secretText, newKey string) (*types.MsgReportBridgeResponse, error) {
	secret, err := base64.StdEncoding.DecodeString(secretText)
	if err != nil || len(secret) != 32 {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "the request secret must be 32 bytes, base64")
	}
	if !req.Paid {
		if nk, err := base64.StdEncoding.DecodeString(newKey); err != nil || len(nk) != 32 || newKey == req.RequestKey {
			return nil, errorsmod.Wrap(types.ErrBridgeRequest, "a fresh 32-byte request key is needed for the next bridge")
		}
		if used, err := k.BridgeRequestByKey.Has(ctx, newKey); err != nil {
			return nil, err
		} else if used {
			return nil, errorsmod.Wrap(types.ErrBridgeRequest, "the new request key is already used")
		}
	}
	consumeGas(ctx, GasPerBridgeProof, "tickets: open bridge answer")
	rubbish, err := AnswerIsRubbish(req.RequestKey, secret, req.Sealed, req.Commitment)
	if err != nil {
		return nil, err
	}
	if !rubbish {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "the answer holds the line the bridge committed to")
	}

	bad := req.BridgeId
	res := &types.MsgReportBridgeResponse{Proven: true}
	// Burn part of the bridge's bond. A bridge with nothing left to burn is
	// still passed over.
	if node, err := k.registryKeeper.GetNode(ctx, bad); err == nil && params.BridgeBadSlashBps > 0 {
		amount := node.Bond.Amount.Mul(sdkmath.NewIntFromUint64(params.BridgeBadSlashBps)).Quo(sdkmath.NewIntFromUint64(types.BasisPoints))
		if amount.IsPositive() {
			burned, err := k.registryKeeper.SlashNode(ctx, bad, amount)
			if err != nil {
				return nil, err
			}
			emit(ctx, EventTypeBridgeBad, sdk.NewAttribute(AttributeKeyRequestID, u64(req.Id)), sdk.NewAttribute(AttributeKeyBridgeID, u64(bad)), sdk.NewAttribute(AttributeKeyAmount, burned.String()))
		}
	} else {
		emit(ctx, EventTypeBridgeBad, sdk.NewAttribute(AttributeKeyRequestID, u64(req.Id)), sdk.NewAttribute(AttributeKeyBridgeID, u64(bad)))
	}

	// The proof published the request's key; what follows is sealed to the
	// device's new key.
	if err := k.BridgeDue.Remove(ctx, collections.Join(req.Deadline, req.Id)); err != nil {
		return nil, err
	}
	req.Bad = append(req.Bad, bad)
	_, height, _ := blockInfo(ctx)
	if req.Paid {
		// Too late to move the payment; the burn is the penalty. Keep the
		// request until it would have been deleted anyway.
		req.State = types.BridgeLapsed
		return res, k.saveRequest(ctx, req)
	}
	// Ask another bridge, if any is left. Bridges already tried, and every
	// bridge proven bad for this request, are skipped.
	next, err := k.pickBridge(ctx, params, bridgeSeed(ctx, req.RequestKey, len(req.Tried)), append(append([]uint64{}, req.Tried...), req.Bad...))
	if err != nil {
		if !errorsmod.IsOf(err, types.ErrNoBridges) {
			return nil, err
		}
		if err := k.distributeFee(ctx, req.Amount); err != nil {
			return nil, err
		}
		req.State = types.BridgeLapsed
		req.Deadline = height + int64(params.RetentionBlocks)
		emit(ctx, EventTypeBridgeLapse, sdk.NewAttribute(AttributeKeyRequestID, u64(req.Id)))
		return res, k.saveRequest(ctx, req)
	}
	if err := k.BridgeRequestByKey.Remove(ctx, req.RequestKey); err != nil {
		return nil, err
	}
	if err := k.BridgeRequestByKey.Set(ctx, newKey, req.Id); err != nil {
		return nil, err
	}
	req.RequestKey = newKey
	req.BridgeId, req.State, req.Sealed, req.Commitment = next, types.BridgeOpen, "", ""
	req.Tried = append(req.Tried, next)
	req.Deadline = height + int64(params.BridgeAnswerBlocks)
	res.Reassigned = next
	emit(ctx, EventTypeBridgeRequest, sdk.NewAttribute(AttributeKeyRequestID, u64(req.Id)), sdk.NewAttribute(AttributeKeyBridgeID, u64(next)))
	return res, k.saveRequest(ctx, req)
}

func (k msgServer) reportDead(ctx context.Context, params types.Params, req types.BridgeRequest, secretText string) (*types.MsgReportBridgeResponse, error) {
	if req.ReportHash == "" {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "the request was made without a report hash")
	}
	if req.Reported {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "already reported")
	}
	secret, err := base64.StdEncoding.DecodeString(secretText)
	if err != nil || len(secret) != 32 {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "the report secret must be 32 bytes, base64")
	}
	want, _ := base64.StdEncoding.DecodeString(req.ReportHash)
	got := sha256.Sum256(secret)
	if subtle.ConstantTimeCompare(got[:], want) != 1 {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "the report secret does not match")
	}
	req.Reported = true
	if err := k.BridgeRequest.Set(ctx, req.Id, req); err != nil {
		return nil, err
	}

	node, err := k.registryKeeper.GetNode(ctx, req.BridgeId)
	if err != nil || node.Commitment != req.Commitment {
		// The bridge has gone or already moved: nothing to count.
		return &types.MsgReportBridgeResponse{}, nil
	}
	h := k.handouts(ctx, node)
	was := deadLine(h, params)
	h.Dead++
	if err := k.BridgeHandouts.Set(ctx, node.Id, h); err != nil {
		return nil, err
	}
	emit(ctx, EventTypeBridgeDead, sdk.NewAttribute(AttributeKeyBridgeID, u64(node.Id)), sdk.NewAttribute(AttributeKeyCount, u64(h.Dead)))
	if !was && deadLine(h, params) {
		// The bridge program watches for this and tells its operator to
		// move: a new port or address gives a new line.
		emit(ctx, EventTypeBridgeStop, sdk.NewAttribute(AttributeKeyBridgeID, u64(node.Id)))
	}
	return &types.MsgReportBridgeResponse{}, nil
}

// BridgeLine says how a bridge's current line stands.
func (q queryServer) BridgeLine(ctx context.Context, req *types.QueryBridgeLineRequest) (*types.QueryBridgeLineResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "empty request")
	}
	node, err := q.k.registryKeeper.GetNode(ctx, req.BridgeId)
	if err != nil {
		return nil, err
	}
	if node.Commitment == "" {
		return nil, errorsmod.Wrapf(types.ErrBridgeRequest, "node %d is not a bridge", req.BridgeId)
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	h := q.k.handouts(ctx, node)
	return &types.QueryBridgeLineResponse{Line: h, Stopped: deadLine(h, params), Full: h.Count >= params.BridgeHandoutLimit}, nil
}
