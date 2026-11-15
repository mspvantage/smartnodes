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

func (q queryServer) ListIssuerKey(ctx context.Context, req *types.QueryAllIssuerKeyRequest) (*types.QueryAllIssuerKeyResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	issuerKeys, pageRes, err := query.CollectionPaginate(
		ctx,
		q.k.IssuerKey,
		req.Pagination,
		func(_ uint64, value types.IssuerKey) (types.IssuerKey, error) {
			return value, nil
		},
	)

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &types.QueryAllIssuerKeyResponse{IssuerKey: issuerKeys, Pagination: pageRes}, nil
}

func (q queryServer) GetIssuerKey(ctx context.Context, req *types.QueryGetIssuerKeyRequest) (*types.QueryGetIssuerKeyResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	issuerKey, err := q.k.IssuerKey.Get(ctx, req.Id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, sdkerrors.ErrKeyNotFound
		}

		return nil, status.Error(codes.Internal, "internal error")
	}

	return &types.QueryGetIssuerKeyResponse{IssuerKey: issuerKey}, nil
}
