package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// A node's service key may redeem its tickets; payment goes to the operator.
func TestServiceKeyRedeemsForTheOperator(t *testing.T) {
	w := newWorld(t)
	wal := w.issue(t, 3)
	service, serviceStr := w.account(t, 40)
	node := w.registry.nodes[nodeA]
	node.ServiceKey = serviceStr
	w.registry.nodes[nodeA] = node

	_, err := w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{
		Operator: serviceStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{wal.spend(t, 0, nodeA)},
	})
	require.NoError(t, err)
	requireInt(t, nodeShare, w.balance(w.opA))
	require.True(t, w.balance(service).IsZero(), "the service key must not be paid")

	// The operator can still redeem too.
	_, err = w.ms.RedeemTickets(w.ctx, &types.MsgRedeemTickets{
		Operator: w.opAStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{wal.spend(t, 1, nodeA)},
	})
	require.NoError(t, err)
	requireInt(t, 2*nodeShare, w.balance(w.opA))

	// A service key works only for its own node.
	_, err = w.ms.RedeemTickets(w.scratch(), &types.MsgRedeemTickets{
		Operator: serviceStr, NodeId: nodeB, KeyId: w.keyID, Tickets: []string{wal.spend(t, 2, nodeB)},
	})
	require.ErrorIs(t, err, types.ErrNotNodeOperator)

	// Once cleared, it can redeem nothing.
	node.ServiceKey = ""
	w.registry.nodes[nodeA] = node
	_, err = w.ms.RedeemTickets(w.scratch(), &types.MsgRedeemTickets{
		Operator: serviceStr, NodeId: nodeA, KeyId: w.keyID, Tickets: []string{wal.spend(t, 2, nodeA)},
	})
	require.ErrorIs(t, err, types.ErrNotNodeOperator)
	w.requireEscrowExact(t)
}
