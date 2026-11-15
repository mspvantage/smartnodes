package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/mspvantage/smartnodes/x/verify/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (q queryServer) ListProber(ctx context.Context, req *types.QueryAllProberRequest) (*types.QueryAllProberResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	probers, pageRes, err := query.CollectionPaginate(
		ctx,
		q.k.Prober,
		req.Pagination,
		func(_ uint64, value types.Prober) (types.Prober, error) {
			return value, nil
		},
	)

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryAllProberResponse{Prober: probers, Pagination: pageRes}, nil
}

func (q queryServer) GetProber(ctx context.Context, req *types.QueryGetProberRequest) (*types.QueryGetProberResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	prober, err := q.k.Prober.Get(ctx, req.Id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, sdkerrors.ErrKeyNotFound
		}

		return nil, status.Error(codes.Internal, "internal error")
	}

	return &types.QueryGetProberResponse{Prober: prober}, nil
}
