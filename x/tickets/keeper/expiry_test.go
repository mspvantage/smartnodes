package keeper_test

import (
	"encoding/binary"
	"testing"
	"time"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/tickets/keeper"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// pastRedemption moves time beyond the test key's 210-day redemption deadline.
const pastRedemption = 211 * day

// spentCount counts spent-ticket records under one issuer key.
func spentCount(t *testing.T, w *world, keyID uint64) int {
	t.Helper()
	n := 0
	err := w.keeper.Spent.Walk(w.ctx, collections.NewPrefixedPairRange[uint64, []byte](keyID), func(collections.Pair[uint64, []byte]) (bool, error) {
		n++
		return false, nil
	})
	require.NoError(t, err)
	return n
}

func TestExpiredKeyIsSweptToTheCommunityPool(t *testing.T) {
	w := newWorld(t)
	wal := w.issue(t, 5)
	_, err := w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{
		Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID,
		Tickets: []string{wal.spend(t, 0, nodeA), wal.spend(t, 1, nodeA)},
	})
	require.NoError(t, err)
	require.Equal(t, 2, spentCount(t, w, w.keyID))
	requireInt(t, 3*ticketPrice+2*proberShare, w.escrow())

	// one block before the deadline: nothing is swept
	w.advanceTime(209 * day)
	w.nextBlocks(t, 1)
	swept, err := w.keeper.SweptKeys.Has(w.ctx, w.keyID)
	require.NoError(t, err)
	require.False(t, swept)
	requireInt(t, 3*ticketPrice+2*proberShare, w.escrow())

	// past the deadline: the three unspent tickets' value goes to the community pool
	w.advanceTime(2 * day)
	w.nextBlocks(t, 1)
	swept, err = w.keeper.SweptKeys.Has(w.ctx, w.keyID)
	require.NoError(t, err)
	require.True(t, swept)
	requireInt(t, 2*communityShare+3*ticketPrice, w.moduleBalance(communityPool))
	requireInt(t, 2*proberShare, w.escrow())
	w.requireEscrowExact(t)

	// its spent records are gone and it is no longer being cleared
	require.Equal(t, 0, spentCount(t, w, w.keyID))
	clearing, err := w.keeper.Clearing.Has(w.ctx, w.keyID)
	require.NoError(t, err)
	require.False(t, clearing)

	// the unspent tickets are now worthless, as the protocol document states
	_, err = w.ms.RedeemTickets(w.scratch(), &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{wal.spend(t, 2, nodeA)}})
	require.ErrorIs(t, err, types.ErrKeyExpired)

	// and running more blocks does not sweep the same key twice
	w.nextBlocks(t, 10)
	requireInt(t, 2*communityShare+3*ticketPrice, w.moduleBalance(communityPool))
	w.requireEscrowExact(t)
}

func TestSweepIsRetriedAfterAFailure(t *testing.T) {
	w := newWorld(t)
	w.issue(t, 4)
	w.advanceTime(pastRedemption)

	// the community pool refuses the first attempt
	w.distr.failNext = true
	w.nextBlocks(t, 1)
	swept, err := w.keeper.SweptKeys.Has(w.ctx, w.keyID)
	require.NoError(t, err)
	require.False(t, swept, "a failed sweep must not be recorded as done")
	requireInt(t, 4*ticketPrice, w.escrow())
	w.requireEscrowExact(t) // the money is still accounted for

	// not retried straight away
	w.nextBlocks(t, 5)
	requireInt(t, 4*ticketPrice, w.escrow())

	// retried after the waiting period
	w.advanceTime(time.Duration(keeper.SweepRetrySeconds) * time.Second)
	w.nextBlocks(t, 1)
	swept, err = w.keeper.SweptKeys.Has(w.ctx, w.keyID)
	require.NoError(t, err)
	require.True(t, swept)
	requireInt(t, 4*ticketPrice, w.moduleBalance(communityPool))
	require.True(t, w.escrow().IsZero())
	w.requireEscrowExact(t)
}

func TestPurchaseCannotBeSignedAfterTheKeyExpires(t *testing.T) {
	w := newWorld(t)
	before := w.balance(w.buyer)
	wal, id := w.buy(t, 2)

	w.advanceTime(pastRedemption)
	_, err := w.ms.FulfilPurchase(w.scratch(), &types.MsgFulfilPurchase{Sender: w.opBStr, Id: id, Signatures: w.iss.sign(t, wal.blinded())})
	require.ErrorIs(t, err, types.ErrKeyExpired)

	// the key is swept with nothing issued, and the buyer is refunded at the deadline
	w.nextBlocks(t, int(types.DefaultIssuanceDeadlineBlocks)+1)
	require.True(t, before.Equal(w.balance(w.buyer)))
	require.True(t, w.escrow().IsZero())
	require.True(t, w.moduleBalance(communityPool).IsZero())
	w.requireEscrowExact(t)
}

// TestSpentRecordsAreClearedInBoundedBatches checks that a key with many
// spent tickets is cleaned up over several blocks, never all at once.
func TestSpentRecordsAreClearedInBoundedBatches(t *testing.T) {
	w := newWorld(t)
	const records = 2*keeper.MaxSpentDeletesPerBlock + 200
	for i := 0; i < records; i++ {
		ticketKey := make([]byte, types.TicketKeySize)
		binary.BigEndian.PutUint64(ticketKey, uint64(i))
		require.NoError(t, w.keeper.Spent.Set(w.ctx, collections.Join(w.keyID, ticketKey)))
	}
	// records under another key must be left alone
	otherKey := w.addKey(t, newIssuer(t, 5))
	require.NoError(t, w.keeper.Spent.Set(w.ctx, collections.Join(otherKey, make([]byte, types.TicketKeySize))))

	// expire only the first key: the second was created later, so push time
	// to just past the first key's deadline
	first, err := w.keeper.GetIssuerKey(w.ctx, w.keyID)
	require.NoError(t, err)
	second, err := w.keeper.GetIssuerKey(w.ctx, otherKey)
	require.NoError(t, err)
	require.Equal(t, first.RedeemEnd, second.RedeemEnd, "both test keys share a deadline; adjust the second")
	second.RedeemEnd += int64(365 * 24 * 3600)
	require.NoError(t, w.keeper.IssuerKey.Set(w.ctx, otherKey, second))

	w.advanceTime(pastRedemption)
	w.nextBlocks(t, 1)
	require.Equal(t, records-keeper.MaxSpentDeletesPerBlock, spentCount(t, w, w.keyID))
	w.nextBlocks(t, 1)
	require.Equal(t, records-2*keeper.MaxSpentDeletesPerBlock, spentCount(t, w, w.keyID))
	w.nextBlocks(t, 1)
	require.Equal(t, 0, spentCount(t, w, w.keyID))

	clearing, err := w.keeper.Clearing.Has(w.ctx, w.keyID)
	require.NoError(t, err)
	require.False(t, clearing)

	// the other key was re-queued for its own deadline, not swept
	require.Equal(t, 1, spentCount(t, w, otherKey))
	swept, err := w.keeper.SweptKeys.Has(w.ctx, otherKey)
	require.NoError(t, err)
	require.False(t, swept)
}

// TestGenesisCarriesSpentTickets is the export gap: a chain restarted from an
// exported genesis must still refuse a ticket that was already paid.
func TestGenesisCarriesSpentTickets(t *testing.T) {
	w := newWorld(t)
	wal := w.issue(t, 3)
	spentTicket := wal.spend(t, 0, nodeA)
	_, err := w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{spentTicket}})
	require.NoError(t, err)

	exported, err := w.keeper.ExportGenesis(w.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.SpentList, 1)
	require.Equal(t, w.keyID, exported.SpentList[0].KeyId)
	require.Len(t, exported.ProberFund, 1)
	require.True(t, exported.ProberFund[0].Amount.Equal(pay(proberShare).Amount))
	require.Empty(t, exported.SweptKeyIds)

	// a brand-new chain started from that export
	f2 := initFixture(t)
	require.NoError(t, f2.keeper.InitGenesis(f2.ctx, *exported))
	f2.bank.modules[types.ModuleName] = sdk.NewCoins(pay(w.escrow().Uint64()))
	f2.addNode(nodeA, w.opAStr, registrytypes.StatusActive)
	ms2 := keeper.NewMsgServerImpl(f2.keeper)
	f2.requireEscrowExact(t)

	// the paid ticket is still refused
	_, err = ms2.RedeemTickets(f2.scratch(), &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{spentTicket}})
	require.ErrorIs(t, err, types.ErrTicketSpent)

	// an unspent one still redeems
	_, err = ms2.RedeemTickets(f2.ctx, &types.MsgRedeemTickets{Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{wal.spend(t, 1, nodeA)}})
	require.NoError(t, err)
	f2.requireEscrowExact(t)

	// and the restarted chain sweeps the key at its deadline like the original would
	f2.advanceTime(pastRedemption)
	f2.nextBlocks(t, 1)
	swept, err := f2.keeper.SweptKeys.Has(f2.ctx, w.keyID)
	require.NoError(t, err)
	require.True(t, swept)
	f2.requireEscrowExact(t)

	// a swept key survives a second export and import
	again, err := f2.keeper.ExportGenesis(f2.ctx)
	require.NoError(t, err)
	require.NoError(t, again.Validate())
	require.Equal(t, []uint64{w.keyID}, again.SweptKeyIds)
	f3 := initFixture(t)
	require.NoError(t, f3.keeper.InitGenesis(f3.ctx, *again))
	f3.bank.modules[types.ModuleName] = sdk.NewCoins(pay(f2.escrow().Uint64()))
	f3.requireEscrowExact(t)
}

func TestGenesisValidateRejectsBadRecords(t *testing.T) {
	base := func() types.GenesisState {
		return types.GenesisState{
			Params:         types.DefaultParams(),
			IssuerKeyList:  []types.IssuerKey{{Id: 0}},
			IssuerKeyCount: 1,
		}
	}
	key32 := make([]byte, types.TicketKeySize)

	good := base()
	good.SpentList = []types.SpentTicket{{KeyId: 0, TicketKey: key32}}
	good.ProberFund = []sdk.Coin{pay(5)}
	good.SweptKeyIds = []uint64{0}
	require.NoError(t, good.Validate())

	tests := []struct {
		name   string
		mutate func(g *types.GenesisState)
	}{
		{"spent ticket key too short", func(g *types.GenesisState) {
			g.SpentList = []types.SpentTicket{{KeyId: 0, TicketKey: key32[:31]}}
		}},
		{"spent ticket under an unknown key", func(g *types.GenesisState) {
			g.SpentList = []types.SpentTicket{{KeyId: 9, TicketKey: key32}}
		}},
		{"spent ticket listed twice", func(g *types.GenesisState) {
			g.SpentList = []types.SpentTicket{{KeyId: 0, TicketKey: key32}, {KeyId: 0, TicketKey: key32}}
		}},
		{"prober fund denom listed twice", func(g *types.GenesisState) { g.ProberFund = []sdk.Coin{pay(5), pay(6)} }},
		{"prober fund of zero", func(g *types.GenesisState) { g.ProberFund = []sdk.Coin{pay(0)} }},
		{"swept key unknown", func(g *types.GenesisState) { g.SweptKeyIds = []uint64{4} }},
		{"swept key listed twice", func(g *types.GenesisState) { g.SweptKeyIds = []uint64{0, 0} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := base()
			tc.mutate(&g)
			require.ErrorIs(t, g.Validate(), types.ErrInvalidGenesis)
		})
	}
}
