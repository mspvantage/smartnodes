package keeper_test

import (
	"bytes"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/stretchr/testify/require"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
	"github.com/mspvantage/smartnodes/x/tickets/keeper"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

const (
	day = 24 * time.Hour
	// A redeemed ticket pays the node 95% and the protocol 5% by default.
	nodeShare = ticketPrice * 95 / 100
	feeShare  = ticketPrice * 5 / 100
)

// world is a fixture with one issuer key registered, a funded buyer and two
// active nodes run by different operators.
type world struct {
	*fixture
	ms       types.MsgServer
	iss      issuer
	keyID    uint64
	buyer    sdk.AccAddress
	buyerStr string
	opA      sdk.AccAddress
	opAStr   string
	opB      sdk.AccAddress
	opBStr   string
}

const (
	nodeA      = uint64(1) // active, operator A
	nodeB      = uint64(2) // active, operator B
	nodeLeaver = uint64(3) // unbonding, operator A
)

func newWorld(t *testing.T) *world {
	t.Helper()
	f := initFixture(t)
	w := &world{fixture: f, ms: keeper.NewMsgServerImpl(f.keeper), iss: newIssuer(t, 1)}
	w.buyer, w.buyerStr = f.account(t, 1)
	w.opA, w.opAStr = f.account(t, 2)
	w.opB, w.opBStr = f.account(t, 3)
	f.fund(w.buyer, 1_000*ticketPrice)
	f.addNode(nodeA, w.opAStr, registrytypes.StatusActive)
	f.addNode(nodeB, w.opBStr, registrytypes.StatusActive)
	f.addNode(nodeLeaver, w.opAStr, registrytypes.StatusUnbonding)
	w.keyID = w.addKey(t, w.iss)
	return w
}

func (w *world) addKey(t *testing.T, iss issuer) uint64 {
	t.Helper()
	now := w.sdkCtx.BlockTime()
	res, err := w.ms.AddIssuerKey(w.ctx, &types.MsgAddIssuerKey{
		Authority: w.authority,
		Pubkey:    b64(iss.pk),
		Price:     pay(ticketPrice),
		SignEnd:   now.Add(30 * day).Unix(),
		RedeemEnd: now.Add(210 * day).Unix(),
	})
	require.NoError(t, err)
	return res.Id
}

// buy sends a purchase for a fresh wallet of n tickets.
func (w *world) buy(t *testing.T, n int) (*wallet, uint64) {
	t.Helper()
	wal := newWallet(t, w.keyID, n)
	res, err := w.ms.BuyTickets(w.ctx, &types.MsgBuyTickets{Buyer: w.buyerStr, KeyId: w.keyID, Blinded: wal.blinded()})
	require.NoError(t, err)
	return wal, res.Id
}

// issue buys n tickets and has the honest issuer sign them, leaving the
// wallet ready to spend.
func (w *world) issue(t *testing.T, n int) *wallet {
	t.Helper()
	wal, id := w.buy(t, n)
	sigs := w.iss.sign(t, wal.blinded())
	_, err := w.ms.FulfilPurchase(w.ctx, &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: sigs})
	require.NoError(t, err)
	wal.unblind(t, w.iss.pk, sigs)
	return wal
}

func (w *world) key(t *testing.T) types.IssuerKey {
	t.Helper()
	k, err := w.keeper.GetIssuerKey(w.ctx, w.keyID)
	require.NoError(t, err)
	return k
}

// ---------------------------------------------------------------------------
// Issuer keys
// ---------------------------------------------------------------------------

func TestAddIssuerKey(t *testing.T) {
	w := newWorld(t)
	now := w.sdkCtx.BlockTime()
	valid := func() *types.MsgAddIssuerKey {
		return &types.MsgAddIssuerKey{
			Authority: w.authority,
			Pubkey:    b64(newIssuer(t, 9).pk),
			Price:     pay(ticketPrice),
			SignEnd:   now.Add(30 * day).Unix(),
			RedeemEnd: now.Add(210 * day).Unix(),
		}
	}

	stored := w.key(t)
	require.Equal(t, b64(w.iss.pk), stored.Pubkey)
	require.Equal(t, uint64(0), stored.Issued)
	require.Equal(t, uint64(0), stored.Redeemed)

	tests := []struct {
		name   string
		mutate func(m *types.MsgAddIssuerKey)
		err    error
	}{
		{"not the authority", func(m *types.MsgAddIssuerKey) { m.Authority = w.buyerStr }, types.ErrInvalidSigner},
		{"public key not base64", func(m *types.MsgAddIssuerKey) { m.Pubkey = "not base64!" }, types.ErrInvalidIssuerKey},
		{"public key wrong length", func(m *types.MsgAddIssuerKey) { m.Pubkey = b64(make([]byte, 48)) }, types.ErrInvalidIssuerKey},
		{"public key is the identity", func(m *types.MsgAddIssuerKey) {
			id := make([]byte, ticketcrypto.PublicKeySize)
			id[0] = 0xc0
			m.Pubkey = b64(id)
		}, types.ErrInvalidIssuerKey},
		{"zero price", func(m *types.MsgAddIssuerKey) { m.Price = pay(0) }, types.ErrInvalidIssuerKey},
		{"sign end in the past", func(m *types.MsgAddIssuerKey) { m.SignEnd = now.Add(-time.Second).Unix() }, types.ErrInvalidIssuerKey},
		{"redeem end before sign end", func(m *types.MsgAddIssuerKey) { m.RedeemEnd = m.SignEnd }, types.ErrInvalidIssuerKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := valid()
			tc.mutate(msg)
			_, err := w.ms.AddIssuerKey(w.scratch(), msg)
			require.ErrorIs(t, err, tc.err)
		})
	}

	res, err := w.ms.AddIssuerKey(w.ctx, valid())
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.Id)
}

// ---------------------------------------------------------------------------
// Buying
// ---------------------------------------------------------------------------

func TestBuyTicketsLocksPayment(t *testing.T) {
	w := newWorld(t)
	before := w.balance(w.buyer)

	wal, id := w.buy(t, 5)
	requireInt(t, 5*ticketPrice, w.escrow())
	require.True(t, before.Sub(w.balance(w.buyer)).Equal(pay(5*ticketPrice).Amount))

	p, err := w.keeper.Purchase.Get(w.ctx, id)
	require.NoError(t, err)
	require.Equal(t, w.buyerStr, p.Buyer)
	require.Equal(t, wal.blinded(), p.Blinded)
	require.Equal(t, w.sdkCtx.BlockHeight()+int64(types.DefaultIssuanceDeadlineBlocks), p.Deadline)
	require.False(t, keeper.IsFulfilled(p))

	// buying does not count as issuance until the issuers sign
	require.Equal(t, uint64(0), w.key(t).Issued)
	w.requireEscrowExact(t)
}

func TestBuyTicketsRejects(t *testing.T) {
	w := newWorld(t)
	_, poor := w.account(t, 99)
	good := newWallet(t, w.keyID, 2).blinded()

	identity := make([]byte, ticketcrypto.G1Size)
	identity[0] = 0xc0

	tooMany := newWallet(t, w.keyID, int(types.DefaultMaxBatch)+1).blinded()

	tests := []struct {
		name string
		msg  *types.MsgBuyTickets
		err  error
	}{
		{"no tickets", &types.MsgBuyTickets{Buyer: w.buyerStr, KeyId: w.keyID}, types.ErrInvalidBatch},
		{"over the batch limit", &types.MsgBuyTickets{Buyer: w.buyerStr, KeyId: w.keyID, Blinded: tooMany}, types.ErrInvalidBatch},
		{"unknown issuer key", &types.MsgBuyTickets{Buyer: w.buyerStr, KeyId: 77, Blinded: good}, types.ErrIssuerKeyNotFound},
		{"not base64", &types.MsgBuyTickets{Buyer: w.buyerStr, KeyId: w.keyID, Blinded: []string{"***"}}, types.ErrInvalidBlinded},
		{"not a curve point", &types.MsgBuyTickets{Buyer: w.buyerStr, KeyId: w.keyID, Blinded: []string{b64(bytes.Repeat([]byte{0xff}, 48))}}, types.ErrInvalidBlinded},
		{"identity point", &types.MsgBuyTickets{Buyer: w.buyerStr, KeyId: w.keyID, Blinded: []string{b64(identity)}}, types.ErrInvalidBlinded},
		{"wrong length", &types.MsgBuyTickets{Buyer: w.buyerStr, KeyId: w.keyID, Blinded: []string{b64(make([]byte, 47))}}, types.ErrInvalidBlinded},
		{"same blinded value twice", &types.MsgBuyTickets{Buyer: w.buyerStr, KeyId: w.keyID, Blinded: []string{good[0], good[0]}}, types.ErrDuplicateBlinded},
		{"buyer cannot pay", &types.MsgBuyTickets{Buyer: poor, KeyId: w.keyID, Blinded: good}, sdkerrors.ErrInsufficientFunds},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := w.ms.BuyTickets(w.scratch(), tc.msg)
			require.ErrorIs(t, err, tc.err)
		})
	}

	t.Run("key has stopped signing", func(t *testing.T) {
		w.advanceTime(31 * day)
		_, err := w.ms.BuyTickets(w.scratch(), &types.MsgBuyTickets{Buyer: w.buyerStr, KeyId: w.keyID, Blinded: good})
		require.ErrorIs(t, err, types.ErrKeyNotSigning)
	})

	require.True(t, w.escrow().IsZero())
	w.requireEscrowExact(t)
}

// ---------------------------------------------------------------------------
// Fulfilment
// ---------------------------------------------------------------------------

func TestFulfilPurchase(t *testing.T) {
	w := newWorld(t)
	wal, id := w.buy(t, 3)
	sigs := w.iss.sign(t, wal.blinded())

	_, err := w.ms.FulfilPurchase(w.ctx, &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: sigs})
	require.NoError(t, err)

	p, err := w.keeper.Purchase.Get(w.ctx, id)
	require.NoError(t, err)
	require.True(t, keeper.IsFulfilled(p))
	require.Equal(t, sigs, p.Signatures)
	require.Equal(t, w.sdkCtx.BlockHeight(), p.FulfilledAt)
	require.Equal(t, uint64(3), w.key(t).Issued)

	// the payment is still in escrow, now backing three issued tickets
	requireInt(t, 3*ticketPrice, w.escrow())
	w.requireEscrowExact(t)

	// the buyer can unblind what the chain stored
	wal.unblind(t, w.iss.pk, p.Signatures)

	_, err = w.ms.FulfilPurchase(w.scratch(), &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: sigs})
	require.ErrorIs(t, err, types.ErrAlreadyFulfilled)
}

func TestFulfilPurchaseRejects(t *testing.T) {
	w := newWorld(t)
	wal, id := w.buy(t, 3)
	good := w.iss.sign(t, wal.blinded())
	impostor := newIssuer(t, 2).sign(t, wal.blinded())

	swapped := []string{good[1], good[0], good[2]}
	oneForged := []string{good[0], impostor[1], good[2]}

	tests := []struct {
		name string
		msg  *types.MsgFulfilPurchase
		err  error
	}{
		{"unknown purchase", &types.MsgFulfilPurchase{Sender: w.opBStr, Id: 99, Signatures: good}, types.ErrPurchaseNotFound},
		{"too few signatures", &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: good[:2]}, types.ErrInvalidBatch},
		{"no signatures", &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id}, types.ErrInvalidBatch},
		{"signed by another key", &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: impostor}, types.ErrBadBlindSignature},
		{"one forged among valid", &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: oneForged}, types.ErrBadBlindSignature},
		{"signatures in the wrong order", &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: swapped}, types.ErrBadBlindSignature},
		{"blinded values passed off as signatures", &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: wal.blinded()}, types.ErrBadBlindSignature},
		{"not base64", &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: []string{"?", "?", "?"}}, types.ErrBadBlindSignature},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := w.ms.FulfilPurchase(w.scratch(), tc.msg)
			require.ErrorIs(t, err, tc.err)
		})
	}

	// nothing was issued by the failed attempts
	require.Equal(t, uint64(0), w.key(t).Issued)
	w.requireEscrowExact(t)
}

// ---------------------------------------------------------------------------
// Refunds and pruning
// ---------------------------------------------------------------------------

func TestUnsignedPurchaseIsRefundedAtTheDeadline(t *testing.T) {
	w := newWorld(t)
	before := w.balance(w.buyer)
	_, id := w.buy(t, 4)
	_, signedID := w.buy(t, 2)

	// the second purchase is signed; only the first should ever be refunded
	p, err := w.keeper.Purchase.Get(w.ctx, signedID)
	require.NoError(t, err)
	_, err = w.ms.FulfilPurchase(w.ctx, &types.MsgFulfilPurchase{Sender: w.opBStr, Id: signedID, Signatures: w.iss.sign(t, p.Blinded)})
	require.NoError(t, err)

	// every block up to the one before the deadline: no refund
	w.nextBlocks(t, int(types.DefaultIssuanceDeadlineBlocks))
	_, err = w.keeper.Purchase.Get(w.ctx, id)
	require.NoError(t, err, "refunded before the deadline")
	requireInt(t, 6*ticketPrice, w.escrow())

	// the deadline block itself
	w.nextBlocks(t, 1)
	_, err = w.keeper.Purchase.Get(w.ctx, id)
	require.Error(t, err, "purchase should be gone after the refund")
	requireInt(t, 2*ticketPrice, w.escrow())
	require.True(t, before.Sub(w.balance(w.buyer)).Equal(pay(2*ticketPrice).Amount), "buyer should be out only the signed purchase")
	w.requireEscrowExact(t)

	// a refunded purchase can no longer be fulfilled
	_, err = w.ms.FulfilPurchase(w.scratch(), &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: []string{"x"}})
	require.ErrorIs(t, err, types.ErrPurchaseNotFound)

	// running many more blocks refunds nothing further
	w.nextBlocks(t, 50)
	requireInt(t, 2*ticketPrice, w.escrow())
	w.requireEscrowExact(t)
}

func TestPurchaseCanStillBeSignedInTheDeadlineBlock(t *testing.T) {
	w := newWorld(t)
	wal, id := w.buy(t, 1)
	w.nextBlocks(t, int(types.DefaultIssuanceDeadlineBlocks)) // now at the deadline height, queue not yet run

	_, err := w.ms.FulfilPurchase(w.ctx, &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: w.iss.sign(t, wal.blinded())})
	require.NoError(t, err)

	w.nextBlocks(t, 5)
	requireInt(t, ticketPrice, w.escrow())
	require.Equal(t, uint64(1), w.key(t).Issued)
	w.requireEscrowExact(t)
}

func TestFulfilledPurchaseIsPrunedButStaysBacked(t *testing.T) {
	w := newWorld(t)
	params := types.DefaultParams()
	params.RetentionBlocks = 20
	require.NoError(t, w.keeper.Params.Set(w.ctx, params))

	wal, id := w.buy(t, 2)
	sigs := w.iss.sign(t, wal.blinded())
	_, err := w.ms.FulfilPurchase(w.ctx, &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: sigs})
	require.NoError(t, err)
	wal.unblind(t, w.iss.pk, sigs)

	w.nextBlocks(t, 20)
	_, err = w.keeper.Purchase.Get(w.ctx, id)
	require.NoError(t, err, "pruned too early")

	w.nextBlocks(t, 1)
	_, err = w.keeper.Purchase.Get(w.ctx, id)
	require.Error(t, err, "record should be pruned")

	// the money did not move, and the tickets still redeem
	requireInt(t, 2*ticketPrice, w.escrow())
	w.requireEscrowExact(t)
	_, err = w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{wal.spend(t, 0, nodeA)}})
	require.NoError(t, err)
	w.requireEscrowExact(t)
}

// ---------------------------------------------------------------------------
// Redemption
// ---------------------------------------------------------------------------

func TestRedeemPaysTheNodeAndTheProtocol(t *testing.T) {
	w := newWorld(t)
	wal := w.issue(t, 4)

	_, err := w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{
		Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID,
		Tickets: []string{wal.spend(t, 0, nodeA), wal.spend(t, 1, nodeA), wal.spend(t, 2, nodeA)},
	})
	require.NoError(t, err)

	requireInt(t, 3*nodeShare, w.balance(w.opA))
	fees, err := w.keeper.AccruedFees.Get(w.ctx, payDenom)
	require.NoError(t, err)
	requireInt(t, 3*feeShare, fees)

	// escrow still holds the unspent ticket plus the fees
	requireInt(t, ticketPrice+3*feeShare, w.escrow())
	k := w.key(t)
	require.Equal(t, uint64(4), k.Issued)
	require.Equal(t, uint64(3), k.Redeemed)
	w.requireEscrowExact(t)
}

func TestRedeemRejectsDoubleSpend(t *testing.T) {
	w := newWorld(t)
	wal := w.issue(t, 2)
	ticket := wal.spend(t, 0, nodeA)

	_, err := w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{ticket}})
	require.NoError(t, err)

	// the same ticket again
	_, err = w.ms.RedeemTickets(w.scratch(), &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{ticket}})
	require.ErrorIs(t, err, types.ErrTicketSpent)

	// the holder signs the same ticket over to a second node: first redemption wins
	_, err = w.ms.RedeemTickets(w.scratch(), &types.MsgRedeemTickets{Operator: w.opBStr, NodeId: nodeB, KeyId: w.keyID, Tickets: []string{wal.spend(t, 0, nodeB)}})
	require.ErrorIs(t, err, types.ErrTicketSpent)

	// the same unspent ticket twice in one batch
	dup := wal.spend(t, 1, nodeA)
	_, err = w.ms.RedeemTickets(w.scratch(), &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{dup, dup}})
	require.ErrorIs(t, err, types.ErrTicketSpent)

	requireInt(t, nodeShare, w.balance(w.opA))
	require.True(t, w.balance(w.opB).IsZero())
	require.Equal(t, uint64(1), w.key(t).Redeemed)
	w.requireEscrowExact(t)
}

func TestRedeemRejects(t *testing.T) {
	w := newWorld(t)
	wal := w.issue(t, 3)
	forA := wal.spend(t, 0, nodeA)
	_, strangerStr := w.account(t, 50)

	// a ticket the issuer never signed for this buyer: self-signed by an impostor
	impostor := newIssuer(t, 2)
	fake := newWallet(t, w.keyID, 1)
	fake.unblind(t, impostor.pk, impostor.sign(t, fake.blinded()))
	forged := fake.spend(t, 0, nodeA)

	// a second, real issuer key with its own tickets
	otherIss := newIssuer(t, 3)
	otherKey := w.addKey(t, otherIss)

	tests := []struct {
		name string
		msg  *types.MsgRedeemTickets
		err  error
	}{
		{"no tickets", &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID}, types.ErrInvalidBatch},
		{"another node redeems a ticket made out to node A", &types.MsgRedeemTickets{Operator: w.opBStr, NodeId: nodeB, KeyId: w.keyID, Tickets: []string{forA}}, types.ErrInvalidTicket},
		{"stranger claims to operate node A", &types.MsgRedeemTickets{Operator: strangerStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{forA}}, types.ErrNotNodeOperator},
		{"operator B claims node A", &types.MsgRedeemTickets{Operator: w.opBStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{forA}}, types.ErrNotNodeOperator},
		{"unknown node", &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: 404, KeyId: w.keyID, Tickets: []string{forA}}, registrytypes.ErrNodeNotFound},
		{"node is unbonding", &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeLeaver, KeyId: w.keyID, Tickets: []string{wal.spend(t, 1, nodeLeaver)}}, types.ErrNodeNotActive},
		{"unknown issuer key", &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: 77, Tickets: []string{forA}}, types.ErrIssuerKeyNotFound},
		{"ticket presented under another issuer key", &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: otherKey, Tickets: []string{forA}}, types.ErrInvalidTicket},
		{"ticket signed by an impostor", &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{forged}}, types.ErrInvalidTicket},
		{"not base64", &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{"@@"}}, types.ErrInvalidTicket},
		{"truncated ticket", &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{b64(make([]byte, 100))}}, types.ErrInvalidTicket},
		{"one bad ticket fails the whole batch", &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{forA, forged}}, types.ErrInvalidTicket},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := w.ms.RedeemTickets(w.scratch(), tc.msg)
			require.ErrorIs(t, err, tc.err)
		})
	}

	t.Run("key past its redemption deadline", func(t *testing.T) {
		w.advanceTime(211 * day)
		_, err := w.ms.RedeemTickets(w.scratch(), &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{forA}})
		require.ErrorIs(t, err, types.ErrKeyExpired)
	})

	// nothing was paid by any of the rejected attempts
	require.True(t, w.balance(w.opA).IsZero())
	require.True(t, w.balance(w.opB).IsZero())
	require.Equal(t, uint64(0), w.key(t).Redeemed)
	w.requireEscrowExact(t)
}

// TestForgedTicketsCannotExceedIssued is the bound on issuer misuse. A holder
// of the issuer key signs tickets nobody paid for. The chain cannot tell them
// from real ones, but it never pays out more tickets than it saw issued.
func TestForgedTicketsCannotExceedIssued(t *testing.T) {
	w := newWorld(t)
	paid := w.issue(t, 2)

	// three tickets signed with the real key, with no purchase behind them
	unpaid := newWallet(t, w.keyID, 3)
	unpaid.unblind(t, w.iss.pk, w.iss.sign(t, unpaid.blinded()))

	redeem := func(ticket string) error {
		_, err := w.ms.RedeemTickets(w.scratch(), &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{ticket}})
		return err
	}
	commit := func(ticket string) {
		_, err := w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{ticket}})
		require.NoError(t, err)
	}

	// two redemptions fit under "issued = 2", whichever tickets they are
	commit(unpaid.spend(t, 0, nodeA))
	commit(unpaid.spend(t, 1, nodeA))

	// the third forged ticket is refused, and so are the honest buyer's tickets:
	// this is the bounded loss described in the protocol document
	require.ErrorIs(t, redeem(unpaid.spend(t, 2, nodeA)), types.ErrOverRedemption)
	require.ErrorIs(t, redeem(paid.spend(t, 0, nodeA)), types.ErrOverRedemption)

	// in total the escrow paid out exactly what was paid in, never more
	k := w.key(t)
	require.Equal(t, k.Issued, k.Redeemed)
	requireInt(t, 2*feeShare, w.escrow())
	requireInt(t, 2*nodeShare, w.balance(w.opA))
	w.requireEscrowExact(t)
}

// ---------------------------------------------------------------------------
// Fees and conservation
// ---------------------------------------------------------------------------

func TestFeeRounding(t *testing.T) {
	w := newWorld(t)
	params := types.DefaultParams()
	params.FeeBps = 333 // 3.33%
	require.NoError(t, w.keeper.Params.Set(w.ctx, params))

	wal := w.issue(t, 3)
	_, err := w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{
		Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID,
		Tickets: []string{wal.spend(t, 0, nodeA), wal.spend(t, 1, nodeA), wal.spend(t, 2, nodeA)},
	})
	require.NoError(t, err)

	fees, err := w.keeper.AccruedFees.Get(w.ctx, payDenom)
	require.NoError(t, err)
	// node payout + fee must equal the ticket value exactly, with no dust lost
	require.True(t, w.balance(w.opA).Add(fees).Equal(pay(3*ticketPrice).Amount))
	requireInt(t, 3*ticketPrice*333/10_000, fees)
	w.requireEscrowExact(t)

	// a zero fee pays the node everything
	params.FeeBps = 0
	require.NoError(t, w.keeper.Params.Set(w.ctx, params))
	wal2 := w.issue(t, 1)
	before := w.balance(w.opB)
	_, err = w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{Operator: w.opBStr, NodeId: nodeB, KeyId: w.keyID, Tickets: []string{wal2.spend(t, 0, nodeB)}})
	require.NoError(t, err)
	require.True(t, w.balance(w.opB).Sub(before).Equal(pay(ticketPrice).Amount))
	w.requireEscrowExact(t)
}

// TestNoCoinIsCreatedOrLost runs a mixed sequence and checks after every step
// that the escrow matches the records and that the total across the buyer,
// the nodes and the escrow never changes.
func TestNoCoinIsCreatedOrLost(t *testing.T) {
	w := newWorld(t)
	total := w.balance(w.buyer)

	conserved := func() {
		t.Helper()
		w.requireEscrowExact(t)
		sum := w.balance(w.buyer).Add(w.balance(w.opA)).Add(w.balance(w.opB)).Add(w.escrow())
		require.True(t, total.Equal(sum), "coins were created or lost: started with %s, now %s", total, sum)
	}

	a := w.issue(t, 10)
	conserved()
	_, abandoned := w.buy(t, 7) // never signed
	conserved()
	b := w.issue(t, 5)
	conserved()

	redeem := func(opStr string, node uint64, tickets ...string) {
		t.Helper()
		_, err := w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{Operator: opStr, NodeId: node, KeyId: w.keyID, Tickets: tickets})
		require.NoError(t, err)
		conserved()
	}

	redeem(w.opAStr, nodeA, a.spend(t, 0, nodeA), a.spend(t, 1, nodeA))
	redeem(w.opBStr, nodeB, a.spend(t, 2, nodeB))
	redeem(w.opBStr, nodeB, b.spend(t, 0, nodeB), b.spend(t, 1, nodeB), b.spend(t, 2, nodeB))

	// let the abandoned purchase reach its deadline and refund
	w.nextBlocks(t, int(types.DefaultIssuanceDeadlineBlocks)+1)
	conserved()
	_, err := w.keeper.Purchase.Get(w.ctx, abandoned)
	require.Error(t, err)

	redeem(w.opAStr, nodeA, a.spend(t, 3, nodeA), b.spend(t, 3, nodeA))

	k := w.key(t)
	require.Equal(t, uint64(15), k.Issued)
	require.Equal(t, uint64(8), k.Redeemed)
	// 7 tickets remain unspent and 8 fees are held
	requireInt(t, 7*ticketPrice+8*feeShare, w.escrow())
}

// TestQueueHandlesManyEntries checks that a burst of expiring purchases is
// worked off over several blocks and that none is lost.
func TestQueueHandlesManyEntries(t *testing.T) {
	w := newWorld(t)
	before := w.balance(w.buyer)
	const purchases = keeper.MaxQueueEntriesPerBlock + 50
	for i := 0; i < purchases; i++ {
		w.buy(t, 1)
	}
	requireInt(t, uint64(purchases)*ticketPrice, w.escrow())

	w.nextBlocks(t, int(types.DefaultIssuanceDeadlineBlocks)+1)
	// one block's allowance has been refunded, the rest wait
	requireInt(t, 50*ticketPrice, w.escrow())
	w.requireEscrowExact(t)

	w.nextBlocks(t, 1)
	require.True(t, w.escrow().IsZero())
	require.True(t, before.Equal(w.balance(w.buyer)))
	w.requireEscrowExact(t)
}

func TestParamsValidate(t *testing.T) {
	require.NoError(t, types.DefaultParams().Validate())
	require.ErrorIs(t, types.Params{}.Validate(), types.ErrInvalidParams)

	bad := []types.Params{
		types.NewParams(0, 100, 500, 100),
		types.NewParams(types.MaxIssuanceDeadlineBlocks+1, 100, 500, 100),
		types.NewParams(100, 0, 500, 100),
		types.NewParams(100, types.HardMaxBatch+1, 500, 100),
		types.NewParams(100, 100, types.MaxFeeBps+1, 100),
		types.NewParams(100, 100, 500, 0),
		types.NewParams(100, 100, 500, types.MaxRetentionBlocks+1),
	}
	for i, p := range bad {
		require.ErrorIs(t, p.Validate(), types.ErrInvalidParams, "case %d", i)
	}
	require.NoError(t, types.NewParams(1, 1, 0, 1).Validate())
	require.NoError(t, types.NewParams(types.MaxIssuanceDeadlineBlocks, types.HardMaxBatch, types.MaxFeeBps, types.MaxRetentionBlocks).Validate())
}
