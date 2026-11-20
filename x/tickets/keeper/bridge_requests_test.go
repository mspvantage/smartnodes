package keeper_test

import (
	"crypto/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/tickets/keeper"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

const (
	bridgeX = uint64(10) // a bridge run by operator A
	bridgeY = uint64(11) // a bridge run by operator B
)

func requestKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b64(b)
}

func sealedAnswer() string { return b64(make([]byte, 120)) }

// bridgeWorld is a world with two bridges and short bridge settings.
func bridgeWorld(t *testing.T) *world {
	w := newWorld(t)
	w.registry.nodes[bridgeX] = registrytypes.Node{Id: bridgeX, Operator: w.opAStr, Tier: registrytypes.TierEntry, Status: registrytypes.StatusActive, Commitment: strings.Repeat("aa", 32)}
	w.registry.nodes[bridgeY] = registrytypes.Node{Id: bridgeY, Operator: w.opBStr, Tier: registrytypes.TierEntry, Status: registrytypes.StatusActive, Commitment: strings.Repeat("bb", 32)}
	p, err := w.keeper.Params.Get(w.ctx)
	require.NoError(t, err)
	p.BridgeAnswerBlocks, p.BridgeTries, p.BridgeHandoutLimit, p.RetentionBlocks = 5, 3, 20, 10
	p.BridgeReportBlocks, p.BridgeDeadReports, p.BridgeDeadPct = 4, 2, 50
	require.NoError(t, w.keeper.Params.Set(w.ctx, p))
	return w
}

func (w *world) operatorOf(id uint64) string { return w.registry.nodes[id].Operator }

func TestBridgeRequestAnswered(t *testing.T) {
	w := bridgeWorld(t)
	wal := w.issue(t, 2)
	rk := requestKey(t)

	res, err := w.ms.RequestBridge(w.ctx, &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: wal.spend(t, 0, types.BridgePoolID), RequestKey: rk})
	require.NoError(t, err)
	req, err := w.keeper.BridgeRequest.Get(w.ctx, res.Id)
	require.NoError(t, err)
	require.Equal(t, types.BridgeOpen, req.State)
	require.Contains(t, []uint64{bridgeX, bridgeY}, req.BridgeId)
	w.requireEscrowExact(t)

	// The same ticket, or a ticket made out to a node, buys nothing.
	_, err = w.ms.RequestBridge(w.scratch(), &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: wal.spend(t, 0, types.BridgePoolID), RequestKey: requestKey(t)})
	require.ErrorIs(t, err, types.ErrTicketSpent)
	_, err = w.ms.RequestBridge(w.scratch(), &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: wal.spend(t, 1, nodeA), RequestKey: requestKey(t)})
	require.ErrorIs(t, err, types.ErrInvalidTicket)
	// A one-time key is used once.
	_, err = w.ms.RequestBridge(w.scratch(), &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: wal.spend(t, 1, types.BridgePoolID), RequestKey: rk})
	require.ErrorIs(t, err, types.ErrBridgeRequest)

	// Only the assigned bridge may answer.
	other := map[uint64]uint64{bridgeX: bridgeY, bridgeY: bridgeX}[req.BridgeId]
	_, err = w.ms.AnswerBridge(w.scratch(), &types.MsgAnswerBridge{Signer: w.operatorOf(other), RequestId: req.Id, Sealed: sealedAnswer()})
	require.ErrorIs(t, err, types.ErrNotNodeOperator)
	_, err = w.ms.AnswerBridge(w.scratch(), &types.MsgAnswerBridge{Signer: w.operatorOf(req.BridgeId), RequestId: req.Id, Sealed: "short"})
	require.ErrorIs(t, err, types.ErrBridgeRequest)

	owner, _ := w.addressCodec.StringToBytes(w.operatorOf(req.BridgeId))
	before := w.balance(owner)
	_, err = w.ms.AnswerBridge(w.ctx, &types.MsgAnswerBridge{Signer: w.operatorOf(req.BridgeId), RequestId: req.Id, Sealed: sealedAnswer()})
	require.NoError(t, err)
	// Paid only once the time to prove a rubbish answer has passed.
	require.True(t, w.balance(owner).Equal(before))
	w.requireEscrowExact(t)
	w.nextBlocks(t, 5)
	requireInt(t, nodeShare, w.balance(owner).Sub(before))
	w.requireEscrowExact(t)

	// The device reads its answer by its key.
	qs := keeper.NewQueryServerImpl(w.keeper)
	got, err := qs.BridgeRequestByKey(w.ctx, &types.QueryBridgeRequestByKeyRequest{RequestKey: rk})
	require.NoError(t, err)
	require.Equal(t, types.BridgeAnswered, got.Request.State)
	require.Equal(t, sealedAnswer(), got.Request.Sealed)
	// Answered once only.
	_, err = w.ms.AnswerBridge(w.scratch(), &types.MsgAnswerBridge{Signer: w.operatorOf(req.BridgeId), RequestId: req.Id, Sealed: sealedAnswer()})
	require.ErrorIs(t, err, types.ErrBridgeRequest)

	// Kept for a while, then deleted.
	w.nextBlocks(t, 12)
	_, err = qs.BridgeRequestByKey(w.ctx, &types.QueryBridgeRequestByKeyRequest{RequestKey: rk})
	require.Error(t, err)
	w.requireEscrowExact(t)
}

func TestBridgeRequestPassesOnThenLapses(t *testing.T) {
	w := bridgeWorld(t)
	wal := w.issue(t, 1)
	res, err := w.ms.RequestBridge(w.ctx, &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: wal.spend(t, 0, types.BridgePoolID), RequestKey: requestKey(t)})
	require.NoError(t, err)
	first, _ := w.keeper.BridgeRequest.Get(w.ctx, res.Id)

	// The first bridge does not answer: the other is asked.
	w.nextBlocks(t, 7)
	second, _ := w.keeper.BridgeRequest.Get(w.ctx, res.Id)
	require.Equal(t, types.BridgeOpen, second.State)
	require.NotEqual(t, first.BridgeId, second.BridgeId)
	require.Len(t, second.Tried, 2)
	qs := keeper.NewQueryServerImpl(w.keeper)
	open, err := qs.BridgeRequestsFor(w.ctx, &types.QueryBridgeRequestsForRequest{BridgeId: second.BridgeId})
	require.NoError(t, err)
	require.Len(t, open.Requests, 1)
	open, _ = qs.BridgeRequestsFor(w.ctx, &types.QueryBridgeRequestsForRequest{BridgeId: first.BridgeId})
	require.Len(t, open.Requests, 0, "the first bridge may no longer answer")
	_, err = w.ms.AnswerBridge(w.scratch(), &types.MsgAnswerBridge{Signer: w.operatorOf(first.BridgeId), RequestId: res.Id, Sealed: sealedAnswer()})
	require.ErrorIs(t, err, types.ErrNotNodeOperator)

	// Neither answers, and no third bridge exists: it lapses, and the
	// payment goes the way of a protocol fee.
	w.nextBlocks(t, 7)
	lapsed, _ := w.keeper.BridgeRequest.Get(w.ctx, res.Id)
	require.Equal(t, types.BridgeLapsed, lapsed.State)
	w.requireEscrowExact(t)
}

func TestBridgeHandoutLimitAndMoving(t *testing.T) {
	w := bridgeWorld(t)
	p, _ := w.keeper.Params.Get(w.ctx)
	p.BridgeHandoutLimit = 1
	require.NoError(t, w.keeper.Params.Set(w.ctx, p))
	delete(w.registry.nodes, bridgeY) // one bridge only
	wal := w.issue(t, 3)

	res, err := w.ms.RequestBridge(w.ctx, &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: wal.spend(t, 0, types.BridgePoolID), RequestKey: requestKey(t)})
	require.NoError(t, err)
	_, err = w.ms.AnswerBridge(w.ctx, &types.MsgAnswerBridge{Signer: w.opAStr, RequestId: res.Id, Sealed: sealedAnswer()})
	require.NoError(t, err)

	// Its line has been given out as often as allowed: nothing to hand out,
	// and the ticket is not spent.
	ticket := wal.spend(t, 1, types.BridgePoolID)
	_, err = w.ms.RequestBridge(w.ctx, &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: ticket, RequestKey: requestKey(t)})
	require.ErrorIs(t, err, types.ErrNoBridges)

	// It moves: a new line, handed out again, and the unspent ticket works.
	n := w.registry.nodes[bridgeX]
	n.Commitment = strings.Repeat("cc", 32)
	w.registry.nodes[bridgeX] = n
	_, err = w.ms.RequestBridge(w.ctx, &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: ticket, RequestKey: requestKey(t)})
	require.NoError(t, err)
	w.requireEscrowExact(t)

	// Listed nodes and leaving bridges are never handed out.
	n.Status = registrytypes.StatusUnbonding
	w.registry.nodes[bridgeX] = n
	_, err = w.ms.RequestBridge(w.scratch(), &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: wal.spend(t, 2, types.BridgePoolID), RequestKey: requestKey(t)})
	require.ErrorIs(t, err, types.ErrNoBridges)
}

func TestBridgeRequestsSurviveExport(t *testing.T) {
	w := bridgeWorld(t)
	wal := w.issue(t, 2)
	res, err := w.ms.RequestBridge(w.ctx, &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: wal.spend(t, 0, types.BridgePoolID), RequestKey: requestKey(t)})
	require.NoError(t, err)
	req, _ := w.keeper.BridgeRequest.Get(w.ctx, res.Id)
	_, err = w.ms.AnswerBridge(w.ctx, &types.MsgAnswerBridge{Signer: w.operatorOf(req.BridgeId), RequestId: res.Id, Sealed: sealedAnswer()})
	require.NoError(t, err)
	_, err = w.ms.RequestBridge(w.ctx, &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: wal.spend(t, 1, types.BridgePoolID), RequestKey: requestKey(t)})
	require.NoError(t, err)

	gen, err := w.keeper.ExportGenesis(w.ctx)
	require.NoError(t, err)
	require.NoError(t, gen.Validate())
	require.Len(t, gen.BridgeRequests, 2)
	require.Equal(t, uint64(2), gen.BridgeRequestCount)
	require.Len(t, gen.BridgeHandouts, 1)

	bad := *gen
	bad.BridgeRequests = append([]types.BridgeRequest{}, gen.BridgeRequests...)
	bad.BridgeRequests[1].RequestKey = bad.BridgeRequests[0].RequestKey
	require.Error(t, bad.Validate())
}
