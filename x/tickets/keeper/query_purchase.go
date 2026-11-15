package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/mspvantage/smartnodes/x/tickets/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (q queryServer) ListPurchase(ctx context.Context, req *types.QueryAllPurchaseRequest) (*types.QueryAllPurchaseResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	purchases, pageRes, err := query.CollectionPaginate(
		ctx,
		q.k.Purchase,
		req.Pagination,
		func(_ uint64, value types.Purchase) (types.Purchase, error) {
			return value, nil
		},
	)

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryAllPurchaseResponse{Purchase: purchases, Pagination: pageRes}, nil
}

func (q queryServer) GetPurchase(ctx context.Context, req *types.QueryGetPurchaseRequest) (*types.QueryGetPurchaseResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	purchase, err := q.k.Purchase.Get(ctx, req.Id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, sdkerrors.ErrKeyNotFound
		}

		return nil, status.Error(codes.Internal, "internal error")
	}

	return &types.QueryGetPurchaseResponse{Purchase: purchase}, nil
}
