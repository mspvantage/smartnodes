package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// Paid bridge hand-out.
//
// A device spends one ticket, made out to the bridge pool, for one bridge
// line. The chain holds the payment and picks a bridge at random from those
// whose current line has been given to fewer than bridge_handout_limit
// people. That bridge's node answers with its line sealed to the device's
// one-time key, which releases the payment to its operator. A bridge that
// does not answer in time is passed over for another; if none answers, the
// payment goes the way of a protocol fee.
//
// Collecting bridges therefore costs a ticket each, and what a censor pays
// to collect them goes to the bridges' operators.

const (
	// Event types and attributes.
	EventTypeBridgeRequest = "bridge_request"
	EventTypeBridgeAnswer  = "bridge_answer"
	EventTypeBridgeLapse   = "bridge_lapse"
	AttributeKeyRequestID  = "request_id"
	AttributeKeyBridgeID   = "bridge_id"

	// maxBridgeDuePerBlock bounds the end-of-block work on bridge requests.
	maxBridgeDuePerBlock = 200
	// minSealed is the shortest possible sealed answer: key, nonce and tag.
	minSealed = 32 + 12 + 16
	// maxSealed bounds a sealed answer; a bridge line is under 300 bytes.
	maxSealed = 1024
)

// handedOut says how often a bridge's current line has been given out.
func (k Keeper) handedOut(ctx context.Context, n registrytypes.Node) (uint64, error) {
	h, err := k.BridgeHandouts.Get(ctx, n.Id)
	if err != nil {
		return 0, nil
	}
	if h.Commitment != n.Commitment {
		return 0, nil // it moved: a new line, given to nobody yet
	}
	return h.Count, nil
}

// pickBridge chooses, from seed, an active bridge not in exclude whose
// current line can still be handed out.
func (k Keeper) pickBridge(ctx context.Context, limit uint64, seed []byte, exclude []uint64) (uint64, error) {
	skip := make(map[uint64]bool, len(exclude))
	for _, id := range exclude {
		skip[id] = true
	}
	var ids []uint64
	err := k.registryKeeper.WalkNodes(ctx, func(n registrytypes.Node) (bool, error) {
		if n.Commitment == "" || n.Status != registrytypes.StatusActive || skip[n.Id] {
			return false, nil
		}
		count, err := k.handedOut(ctx, n)
		if err != nil {
			return true, err
		}
		if count < limit {
			ids = append(ids, n.Id)
		}
		return false, nil
	})
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, types.ErrNoBridges
	}
	sum := sha256.Sum256(seed)
	return ids[binary.BigEndian.Uint64(sum[:8])%uint64(len(ids))], nil
}

// bridgeSeed mixes the block with the request, so the pick cannot be
// steered by the requester alone.
func bridgeSeed(ctx context.Context, requestKey string, round int) []byte {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	var b bytes.Buffer
	b.WriteString("SMARTNODES-BRIDGE-PICK-V1")
	b.Write(sdkCtx.HeaderHash())
	_ = binary.Write(&b, binary.BigEndian, sdkCtx.BlockHeight())
	b.WriteString(requestKey)
	_ = binary.Write(&b, binary.BigEndian, int64(round))
	return b.Bytes()
}

// RequestBridge spends a ticket made out to the bridge pool for one line.
func (k msgServer) RequestBridge(ctx context.Context, msg *types.MsgRequestBridge) (*types.MsgRequestBridgeResponse, error) {
	if _, err := k.addressCodec.StringToBytes(msg.Signer); err != nil {
		return nil, errorsmod.Wrap(err, "invalid signer address")
	}
	if rk, err := base64.StdEncoding.DecodeString(msg.RequestKey); err != nil || len(rk) != 32 {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "request key must be 32 bytes, base64")
	}
	if used, err := k.BridgeRequestByKey.Has(ctx, msg.RequestKey); err != nil {
		return nil, err
	} else if used {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "request key already used; make a fresh one")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	// Pick first: if no bridge can take it, nothing is spent.
	bridgeID, err := k.pickBridge(ctx, params.BridgeHandoutLimit, bridgeSeed(ctx, msg.RequestKey, 0), nil)
	if err != nil {
		return nil, err
	}

	key, err := k.GetIssuerKey(ctx, msg.KeyId)
	if err != nil {
		return nil, err
	}
	now, height, chainID := blockInfo(ctx)
	if now >= key.RedeemEnd {
		return nil, errorsmod.Wrapf(types.ErrKeyExpired, "key %d", key.Id)
	}
	pubkey, err := decodeBase64(key.Pubkey)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidIssuerKey, "stored public key is not valid base64")
	}
	raw, err := decodeBase64(msg.Ticket)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidTicket, "not valid base64")
	}
	consumeGas(ctx, GasPerPairingCheck, "tickets: verify bridge ticket")
	ticketKey, err := ticketcrypto.VerifyTicket(pubkey, chainID, key.Id, types.BridgePoolID, raw)
	if err != nil {
		return nil, errorsmod.Wrapf(types.ErrInvalidTicket, "%s", err)
	}
	spentKey := collections.Join(key.Id, ticketKey)
	if spent, err := k.Spent.Has(ctx, spentKey); err != nil {
		return nil, err
	} else if spent {
		return nil, types.ErrTicketSpent
	}
	if err := k.Spent.Set(ctx, spentKey); err != nil {
		return nil, err
	}
	key.Redeemed++
	if key.Redeemed > key.Issued {
		return nil, errorsmod.Wrapf(types.ErrOverRedemption, "key %d: %d issued", key.Id, key.Issued)
	}
	if err := k.IssuerKey.Set(ctx, key.Id, key); err != nil {
		return nil, err
	}

	// The ticket's value stays in this module's account, now held for the
	// request instead of for an unspent ticket.
	id, err := k.BridgeRequestSeq.Next(ctx)
	if err != nil {
		return nil, err
	}
	req := types.BridgeRequest{
		Id: id, RequestKey: msg.RequestKey, Amount: key.Price, BridgeId: bridgeID,
		Deadline: height + int64(params.BridgeAnswerBlocks), Tried: []uint64{bridgeID}, State: types.BridgeOpen,
	}
	if err := k.saveRequest(ctx, req); err != nil {
		return nil, err
	}
	if err := k.BridgeRequestByKey.Set(ctx, msg.RequestKey, id); err != nil {
		return nil, err
	}
	emit(ctx, EventTypeBridgeRequest, sdk.NewAttribute(AttributeKeyRequestID, u64(id)), sdk.NewAttribute(AttributeKeyBridgeID, u64(bridgeID)))
	return &types.MsgRequestBridgeResponse{Id: id}, nil
}

// saveRequest stores a request and queues it for its deadline.
func (k Keeper) saveRequest(ctx context.Context, req types.BridgeRequest) error {
	if err := k.BridgeRequest.Set(ctx, req.Id, req); err != nil {
		return err
	}
	return k.BridgeDue.Set(ctx, collections.Join(req.Deadline, req.Id))
}

// AnswerBridge stores a bridge's sealed answer and pays its operator.
func (k msgServer) AnswerBridge(ctx context.Context, msg *types.MsgAnswerBridge) (*types.MsgAnswerBridgeResponse, error) {
	signer, err := k.addressCodec.StringToBytes(msg.Signer)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid signer address")
	}
	req, err := k.BridgeRequest.Get(ctx, msg.RequestId)
	if err != nil {
		return nil, errorsmod.Wrapf(types.ErrBridgeRequest, "no request %d", msg.RequestId)
	}
	if req.State != types.BridgeOpen {
		return nil, errorsmod.Wrapf(types.ErrBridgeRequest, "request %d is no longer open", req.Id)
	}
	node, err := k.registryKeeper.GetNode(ctx, req.BridgeId)
	if err != nil {
		return nil, err
	}
	operator, err := k.addressCodec.StringToBytes(node.Operator)
	if err != nil {
		return nil, err
	}
	allowed := bytes.Equal(signer, operator)
	if !allowed && node.ServiceKey != "" {
		service, err := k.addressCodec.StringToBytes(node.ServiceKey)
		allowed = err == nil && bytes.Equal(signer, service)
	}
	if !allowed {
		return nil, errorsmod.Wrapf(types.ErrNotNodeOperator, "request %d is for bridge %d", req.Id, req.BridgeId)
	}
	if node.Status != registrytypes.StatusActive || node.Commitment == "" {
		return nil, errorsmod.Wrapf(types.ErrNodeNotActive, "bridge %d", node.Id)
	}
	sealed, err := base64.StdEncoding.DecodeString(msg.Sealed)
	if err != nil || len(sealed) < minSealed || len(sealed) > maxSealed {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "the sealed answer has the wrong form")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}

	// Answered: kept for the device to read, then deleted.
	_, height, _ := blockInfo(ctx)
	if err := k.BridgeDue.Remove(ctx, collections.Join(req.Deadline, req.Id)); err != nil {
		return nil, err
	}
	req.Sealed, req.State = msg.Sealed, types.BridgeAnswered
	req.Deadline = height + int64(params.RetentionBlocks)
	if err := k.saveRequest(ctx, req); err != nil {
		return nil, err
	}

	count, err := k.handedOut(ctx, node)
	if err != nil {
		return nil, err
	}
	if err := k.BridgeHandouts.Set(ctx, node.Id, types.BridgeHandouts{BridgeId: node.Id, Commitment: node.Commitment, Count: count + 1}); err != nil {
		return nil, err
	}

	// Paid like a redeemed ticket: the price less the protocol fee.
	fee := req.Amount.Amount.Mul(sdkmath.NewIntFromUint64(params.FeeBps)).Quo(sdkmath.NewIntFromUint64(types.BasisPoints))
	if err := k.distributeFee(ctx, sdk.NewCoin(req.Amount.Denom, fee)); err != nil {
		return nil, err
	}
	payout := sdk.NewCoin(req.Amount.Denom, req.Amount.Amount.Sub(fee))
	if payout.IsPositive() {
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, operator, sdk.NewCoins(payout)); err != nil {
			return nil, err
		}
	}
	emit(ctx, EventTypeBridgeAnswer, sdk.NewAttribute(AttributeKeyRequestID, u64(req.Id)), sdk.NewAttribute(AttributeKeyBridgeID, u64(node.Id)))
	return &types.MsgAnswerBridgeResponse{}, nil
}

// ProcessBridgeDue passes unanswered requests to another bridge, lapses
// those no bridge answered, and deletes old ones. One entry's failure never
// stops the chain or the others.
func (k Keeper) ProcessBridgeDue(ctx context.Context) error {
	_, height, _ := blockInfo(ctx)
	due := new(collections.Range[collections.Pair[int64, uint64]]).EndInclusive(collections.Join(height, uint64(1<<64-1)))
	iter, err := k.BridgeDue.Iterate(ctx, due)
	if err != nil {
		return err
	}
	var keys []collections.Pair[int64, uint64]
	for ; iter.Valid() && len(keys) < maxBridgeDuePerBlock; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return err
		}
		keys = append(keys, key)
	}
	iter.Close()

	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	for _, key := range keys {
		cache, write := sdkCtx.CacheContext()
		if err := k.bridgeDue(cache, params, key); err != nil {
			emit(ctx, "bridge_due_error", sdk.NewAttribute(AttributeKeyRequestID, u64(key.K2())), sdk.NewAttribute("error", err.Error()))
			// Drop the entry so it cannot fail again every block.
			_ = k.BridgeDue.Remove(ctx, key)
			continue
		}
		write()
	}
	return nil
}

func (k Keeper) bridgeDue(ctx context.Context, params types.Params, key collections.Pair[int64, uint64]) error {
	if err := k.BridgeDue.Remove(ctx, key); err != nil {
		return err
	}
	req, err := k.BridgeRequest.Get(ctx, key.K2())
	if err != nil {
		return nil // already gone
	}
	_, height, _ := blockInfo(ctx)
	if req.State != types.BridgeOpen {
		// Kept long enough to be read: delete.
		if err := k.BridgeRequest.Remove(ctx, req.Id); err != nil {
			return err
		}
		return k.BridgeRequestByKey.Remove(ctx, req.RequestKey)
	}
	// Unanswered in time: another bridge, if any is left to ask.
	if uint64(len(req.Tried)) < params.BridgeTries {
		next, err := k.pickBridge(ctx, params.BridgeHandoutLimit, bridgeSeed(ctx, req.RequestKey, len(req.Tried)), req.Tried)
		if err == nil {
			req.BridgeId = next
			req.Tried = append(req.Tried, next)
			req.Deadline = height + int64(params.BridgeAnswerBlocks)
			emit(ctx, EventTypeBridgeRequest, sdk.NewAttribute(AttributeKeyRequestID, u64(req.Id)), sdk.NewAttribute(AttributeKeyBridgeID, u64(next)))
			return k.saveRequest(ctx, req)
		}
		if !errorsmod.IsOf(err, types.ErrNoBridges) {
			return err
		}
	}
	// No bridge answered. The payment cannot be refunded to an anonymous
	// ticket, so it goes the way of a protocol fee.
	if err := k.distributeFee(ctx, req.Amount); err != nil {
		return err
	}
	req.State = types.BridgeLapsed
	req.Deadline = height + int64(params.RetentionBlocks)
	emit(ctx, EventTypeBridgeLapse, sdk.NewAttribute(AttributeKeyRequestID, u64(req.Id)))
	return k.saveRequest(ctx, req)
}

// BridgeRequestByKey finds a request by its one-time key.
func (q queryServer) BridgeRequestByKey(ctx context.Context, req *types.QueryBridgeRequestByKeyRequest) (*types.QueryBridgeRequestByKeyResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "empty request")
	}
	id, err := q.k.BridgeRequestByKey.Get(ctx, req.RequestKey)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "no request with that key")
	}
	r, err := q.k.BridgeRequest.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &types.QueryBridgeRequestByKeyResponse{Request: r}, nil
}

// BridgeRequestsFor lists the open requests a bridge must answer.
func (q queryServer) BridgeRequestsFor(ctx context.Context, req *types.QueryBridgeRequestsForRequest) (*types.QueryBridgeRequestsForResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrBridgeRequest, "empty request")
	}
	res := &types.QueryBridgeRequestsForResponse{}
	err := q.k.BridgeRequest.Walk(ctx, nil, func(_ uint64, r types.BridgeRequest) (bool, error) {
		if r.State == types.BridgeOpen && r.BridgeId == req.BridgeId {
			res.Requests = append(res.Requests, r)
		}
		return false, nil
	})
	return res, err
}
