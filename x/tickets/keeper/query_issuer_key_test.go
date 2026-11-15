package keeper_test

import (
	"context"
	"strconv"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mspvantage/smartnodes/x/tickets/keeper"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

func createNIssuerKey(keeper keeper.Keeper, ctx context.Context, n int) []types.IssuerKey {
	items := make([]types.IssuerKey, n)
	for i := range items {
		iu := uint64(i)
		items[i].Id = iu
		items[i].Pubkey = strconv.Itoa(i)
		items[i].Price = sdk.NewInt64Coin(`token`, int64(i+100))
		items[i].SignEnd = int64(i)
		items[i].RedeemEnd = int64(i)
		items[i].Issued = uint64(i)
		items[i].Redeemed = uint64(i)
		_ = keeper.IssuerKey.Set(ctx, iu, items[i])
		_ = keeper.IssuerKeySeq.Set(ctx, iu)
	}
	return items
}

func TestIssuerKeyQuerySingle(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)
	msgs := createNIssuerKey(f.keeper, f.ctx, 2)
	tests := []struct {
		desc     string
		request  *types.QueryGetIssuerKeyRequest
		response *types.QueryGetIssuerKeyResponse
		err      error
	}{
		{
			desc:     "First",
			request:  &types.QueryGetIssuerKeyRequest{Id: msgs[0].Id},
			response: &types.QueryGetIssuerKeyResponse{IssuerKey: msgs[0]},
		},
		{
			desc:     "Second",
			request:  &types.QueryGetIssuerKeyRequest{Id: msgs[1].Id},
			response: &types.QueryGetIssuerKeyResponse{IssuerKey: msgs[1]},
		},
		{
			desc:    "KeyNotFound",
			request: &types.QueryGetIssuerKeyRequest{Id: uint64(len(msgs))},
			err:     sdkerrors.ErrKeyNotFound,
		},
		{
			desc: "InvalidRequest",
			err:  status.Error(codes.InvalidArgument, "invalid request"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			response, err := qs.GetIssuerKey(f.ctx, tc.request)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
				require.EqualExportedValues(t, tc.response, response)
			}
		})
	}
}

func TestIssuerKeyQueryPaginated(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)
	msgs := createNIssuerKey(f.keeper, f.ctx, 5)

	request := func(next []byte, offset, limit uint64, total bool) *types.QueryAllIssuerKeyRequest {
		return &types.QueryAllIssuerKeyRequest{
			Pagination: &query.PageRequest{
				Key:        next,
				Offset:     offset,
				Limit:      limit,
				CountTotal: total,
			},
		}
	}
	t.Run("ByOffset", func(t *testing.T) {
		step := 2
		for i := 0; i < len(msgs); i += step {
			resp, err := qs.ListIssuerKey(f.ctx, request(nil, uint64(i), uint64(step), false))
			require.NoError(t, err)
			require.LessOrEqual(t, len(resp.IssuerKey), step)
			require.Subset(t, msgs, resp.IssuerKey)
		}
	})
	t.Run("ByKey", func(t *testing.T) {
		step := 2
		var next []byte
		for i := 0; i < len(msgs); i += step {
			resp, err := qs.ListIssuerKey(f.ctx, request(next, 0, uint64(step), false))
			require.NoError(t, err)
			require.LessOrEqual(t, len(resp.IssuerKey), step)
			require.Subset(t, msgs, resp.IssuerKey)
			next = resp.Pagination.NextKey
		}
	})
	t.Run("Total", func(t *testing.T) {
		resp, err := qs.ListIssuerKey(f.ctx, request(nil, 0, 0, true))
		require.NoError(t, err)
		require.Equal(t, len(msgs), int(resp.Pagination.Total))
		require.EqualExportedValues(t, msgs, resp.IssuerKey)
	})
	t.Run("InvalidRequest", func(t *testing.T) {
		_, err := qs.ListIssuerKey(f.ctx, nil)
		require.ErrorIs(t, err, status.Error(codes.InvalidArgument, "invalid request"))
	})
}
