package keeper

import (
	"context"
	"encoding/hex"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

// TicketSpent reports whether a ticket has been redeemed. Nodes ask before
// they serve a ticket, so one that was already used is refused at once.
func (q queryServer) TicketSpent(ctx context.Context, req *types.QueryTicketSpentRequest) (*types.QueryTicketSpentResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	ticketKey, err := hex.DecodeString(req.TicketKey)
	if err != nil || len(ticketKey) != ticketcrypto.TicketKeySize {
		return nil, status.Error(codes.InvalidArgument, "ticket_key must be 64 hex characters")
	}
	spent, err := q.k.Spent.Has(ctx, collections.Join(req.KeyId, ticketKey))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryTicketSpentResponse{Spent: spent}, nil
}
