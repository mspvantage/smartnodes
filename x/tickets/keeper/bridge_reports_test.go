package keeper_test

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/vpn/bridge"
	"github.com/mspvantage/smartnodes/vpn/disguise"
	"github.com/mspvantage/smartnodes/x/tickets/keeper"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// realBridges gives both bridges real lines and bonds, and returns the lines.
func realBridges(t *testing.T, w *world) map[uint64]bridge.Line {
	t.Helper()
	lines := map[uint64]bridge.Line{}
	for i, id := range []uint64{bridgeX, bridgeY} {
		k, err := disguise.NewServerKeys()
		require.NoError(t, err)
		wg := make([]byte, 32)
		_, _ = rand.Read(wg)
		l := bridge.Line{ChainID: "smartnodes", NodeID: id, Address: netip.AddrFrom4([4]byte{203, 0, 113, byte(10 + i)}), Port: 51830,
			WGKey: base64.StdEncoding.EncodeToString(wg), Reality: k.Published(443, "www.example.com")}
		require.NoError(t, l.Validate())
		n := w.registry.nodes[id]
		n.Commitment = l.Fingerprint()
		n.Bond = pay(1_000_000)
		w.registry.nodes[id] = n
		lines[id] = l
	}
	return lines
}

type device struct {
	key    *ecdh.PrivateKey
	secret []byte
}

func newDevice(t *testing.T) device {
	t.Helper()
	k, err := bridge.NewRequestKey()
	require.NoError(t, err)
	s := make([]byte, 32)
	_, _ = rand.Read(s)
	return device{key: k, secret: s}
}

func (d device) request(w *world, t *testing.T, ticket string) types.BridgeRequest {
	t.Helper()
	h := sha256.Sum256(d.secret)
	res, err := w.ms.RequestBridge(w.ctx, &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: ticket,
		RequestKey: bridge.B64(d.key.PublicKey().Bytes()), ReportHash: bridge.B64(h[:])})
	require.NoError(t, err)
	req, err := w.keeper.BridgeRequest.Get(w.ctx, res.Id)
	require.NoError(t, err)
	return req
}

func (w *world) answer(t *testing.T, req types.BridgeRequest, to *ecdh.PrivateKey, line string) {
	t.Helper()
	sealed, err := bridge.Seal(to.PublicKey().Bytes(), line)
	require.NoError(t, err)
	_, err = w.ms.AnswerBridge(w.ctx, &types.MsgAnswerBridge{Signer: w.operatorOf(req.BridgeId), RequestId: req.Id, Sealed: bridge.B64(sealed)})
	require.NoError(t, err)
}

func TestRubbishAnswerProvenAndReplaced(t *testing.T) {
	w := bridgeWorld(t)
	lines := realBridges(t, w)
	wal := w.issue(t, 1)
	d := newDevice(t)
	req := d.request(w, t, wal.spend(t, 0, types.BridgePoolID))
	bad := req.BridgeId
	other := map[uint64]uint64{bridgeX: bridgeY, bridgeY: bridgeX}[bad]
	owner, _ := w.addressCodec.StringToBytes(w.operatorOf(bad))
	before := w.balance(owner)

	// The bridge answers with another bridge's line: valid, but not its own.
	w.answer(t, req, d.key, lines[other].String())
	secret := bridge.B64(d.key.Bytes())

	// Someone else's key proves nothing.
	stranger := newDevice(t)
	_, err := w.ms.ReportBridge(w.scratch(), &types.MsgReportBridge{Signer: w.opBStr, RequestId: req.Id, RequestSecret: bridge.B64(stranger.key.Bytes())})
	require.ErrorIs(t, err, types.ErrBridgeRequest)

	// The proof needs a fresh key for the next answer.
	_, err = w.ms.ReportBridge(w.scratch(), &types.MsgReportBridge{Signer: w.opBStr, RequestId: req.Id, RequestSecret: secret})
	require.ErrorIs(t, err, types.ErrBridgeRequest)
	next := newDevice(t)
	res, err := w.ms.ReportBridge(w.ctx, &types.MsgReportBridge{Signer: w.opBStr, RequestId: req.Id, RequestSecret: secret, NewRequestKey: bridge.B64(next.key.PublicKey().Bytes())})
	require.NoError(t, err)
	require.True(t, res.Proven)
	require.Equal(t, other, res.Reassigned)
	requireInt(t, 900_000, w.registry.nodes[bad].Bond.Amount)
	w.requireEscrowExact(t)

	// The rubbish bridge is never paid, and the other bridge now answers.
	got, _ := w.keeper.BridgeRequest.Get(w.ctx, req.Id)
	require.Equal(t, types.BridgeOpen, got.State)
	require.Equal(t, []uint64{bad}, got.Bad)
	require.Equal(t, bridge.B64(next.key.PublicKey().Bytes()), got.RequestKey)
	w.answer(t, got, next.key, lines[other].String())
	w.nextBlocks(t, 6)
	require.True(t, w.balance(owner).Equal(before))
	got, _ = w.keeper.BridgeRequest.Get(w.ctx, req.Id)
	require.True(t, got.Paid)
	w.requireEscrowExact(t)

	// A good answer cannot be reported as rubbish.
	_, err = w.ms.ReportBridge(w.scratch(), &types.MsgReportBridge{Signer: w.opBStr, RequestId: req.Id, RequestSecret: bridge.B64(next.key.Bytes())})
	require.ErrorIs(t, err, types.ErrBridgeRequest)
}

func TestGarbageAnswerProven(t *testing.T) {
	w := bridgeWorld(t)
	realBridges(t, w)
	delete(w.registry.nodes, bridgeY)
	wal := w.issue(t, 1)
	d := newDevice(t)
	req := d.request(w, t, wal.spend(t, 0, types.BridgePoolID))
	// Bytes that do not open at all.
	_, err := w.ms.AnswerBridge(w.ctx, &types.MsgAnswerBridge{Signer: w.operatorOf(req.BridgeId), RequestId: req.Id, Sealed: sealedAnswer()})
	require.NoError(t, err)
	res, err := w.ms.ReportBridge(w.ctx, &types.MsgReportBridge{Signer: w.opBStr, RequestId: req.Id, RequestSecret: bridge.B64(d.key.Bytes()), NewRequestKey: requestKey(t)})
	require.NoError(t, err)
	require.True(t, res.Proven)
	// No other bridge: the payment goes the way of a fee, never to the
	// rubbish bridge.
	require.Zero(t, res.Reassigned)
	got, _ := w.keeper.BridgeRequest.Get(w.ctx, req.Id)
	require.Equal(t, types.BridgeLapsed, got.State)
	w.requireEscrowExact(t)
}

func TestDeadReportsStopHandout(t *testing.T) {
	w := bridgeWorld(t)
	lines := realBridges(t, w)
	delete(w.registry.nodes, bridgeY)
	wal := w.issue(t, 5)

	var devs []device
	var reqs []types.BridgeRequest
	for i := 0; i < 4; i++ {
		d := newDevice(t)
		r := d.request(w, t, wal.spend(t, i, types.BridgePoolID))
		w.answer(t, r, d.key, lines[bridgeX].String())
		devs, reqs = append(devs, d), append(reqs, r)
	}
	report := func(i int, secret []byte) error {
		_, err := w.ms.ReportBridge(w.ctx, &types.MsgReportBridge{Signer: w.opBStr, RequestId: reqs[i].Id, ReportSecret: bridge.B64(secret)})
		return err
	}
	// The wrong secret, or both kinds at once, count for nothing.
	require.ErrorIs(t, report(0, devs[1].secret), types.ErrBridgeRequest)
	_, err := w.ms.ReportBridge(w.scratch(), &types.MsgReportBridge{Signer: w.opBStr, RequestId: reqs[0].Id, ReportSecret: bridge.B64(devs[0].secret), RequestSecret: bridge.B64(devs[0].key.Bytes())})
	require.ErrorIs(t, err, types.ErrBridgeRequest)

	// One report of four is not enough; the same device twice is one.
	require.NoError(t, report(0, devs[0].secret))
	require.ErrorIs(t, report(0, devs[0].secret), types.ErrBridgeRequest)
	d := newDevice(t)
	r := d.request(w, t, wal.spend(t, 4, types.BridgePoolID))
	require.Equal(t, bridgeX, r.BridgeId)

	// Two reports of four given the line: at least 2, and 50%. Enough.
	require.NoError(t, report(1, devs[1].secret))
	h, _ := w.keeper.BridgeHandouts.Get(w.ctx, bridgeX)
	require.Equal(t, uint64(2), h.Dead)
	require.NoError(t, report(2, devs[2].secret))
	h, _ = w.keeper.BridgeHandouts.Get(w.ctx, bridgeX)
	require.Equal(t, uint64(3), h.Dead)

	line, err := keeper.NewQueryServerImpl(w.keeper).BridgeLine(w.ctx, &types.QueryBridgeLineRequest{BridgeId: bridgeX})
	require.NoError(t, err)
	require.True(t, line.Stopped)
	require.Equal(t, uint64(4), line.Line.Count)

	// The line is no longer handed out, until the bridge moves.
	more := w.issue(t, 1)
	_, err = w.ms.RequestBridge(w.scratch(), &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: more.spend(t, 0, types.BridgePoolID), RequestKey: requestKey(t)})
	require.ErrorIs(t, err, types.ErrNoBridges)
	n := w.registry.nodes[bridgeX]
	n.Commitment = lines[bridgeX].Fingerprint()[:62] + "00"
	w.registry.nodes[bridgeX] = n
	_, err = w.ms.RequestBridge(w.ctx, &types.MsgRequestBridge{Signer: w.opAStr, KeyId: w.keyID, Ticket: more.spend(t, 0, types.BridgePoolID), RequestKey: requestKey(t)})
	require.NoError(t, err)
	// A report on the old line no longer counts against the new one.
	// (The stored record still holds the old line's counts until the new
	// line is first handed out; the chain reads it as a fresh line.)
	require.NoError(t, report(3, devs[3].secret))
	line, err = keeper.NewQueryServerImpl(w.keeper).BridgeLine(w.ctx, &types.QueryBridgeLineRequest{BridgeId: bridgeX})
	require.NoError(t, err)
	require.Zero(t, line.Line.Dead)
	require.False(t, line.Stopped)
	w.requireEscrowExact(t)
}

func TestAnswerIsRubbish(t *testing.T) {
	w := bridgeWorld(t)
	lines := realBridges(t, w)
	d := newDevice(t)
	rk := bridge.B64(d.key.PublicKey().Bytes())
	sealed, err := bridge.Seal(d.key.PublicKey().Bytes(), lines[bridgeX].String())
	require.NoError(t, err)
	for name, tc := range map[string]struct {
		commitment string
		want       bool
	}{
		"its own line":  {lines[bridgeX].Fingerprint(), false},
		"another line":  {lines[bridgeY].Fingerprint(), true},
		"no commitment": {"", true},
	} {
		got, err := keeper.AnswerIsRubbish(rk, d.key.Bytes(), bridge.B64(sealed), tc.commitment)
		require.NoError(t, err, name)
		require.Equal(t, tc.want, got, name)
	}
}
