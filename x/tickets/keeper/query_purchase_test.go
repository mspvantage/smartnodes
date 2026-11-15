package keeper_test

import (
	"context"
	"fmt"
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

func createNPurchase(keeper keeper.Keeper, ctx context.Context, n int) []types.Purchase {
	items := make([]types.Purchase, n)
	for i := range items {
		iu := uint64(i)
		items[i].Id = iu
		items[i].Buyer = fmt.Sprintf(`cosmos1abcdef%d`, i)
		items[i].KeyId = uint64(i)
		items[i].Blinded = []string{`abc` + strconv.Itoa(i), `xyz` + strconv.Itoa(i)}
		items[i].Locked = sdk.NewInt64Coin(`token`, int64(i+100))
		items[i].Deadline = int64(i)
		items[i].Signatures = []string{`abc` + strconv.Itoa(i), `xyz` + strconv.Itoa(i)}
		items[i].FulfilledAt = int64(i)
		_ = keeper.Purchase.Set(ctx, iu, items[i])
		_ = keeper.PurchaseSeq.Set(ctx, iu)
	}
	return items
}

func TestPurchaseQuerySingle(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)
	msgs := createNPurchase(f.keeper, f.ctx, 2)
	tests := []struct {
		desc     string
		request  *types.QueryGetPurchaseRequest
		response *types.QueryGetPurchaseResponse
		err      error
	}{
		{
			desc:     "First",
			request:  &types.QueryGetPurchaseRequest{Id: msgs[0].Id},
			response: &types.QueryGetPurchaseResponse{Purchase: msgs[0]},
		},
		{
			desc:     "Second",
			request:  &types.QueryGetPurchaseRequest{Id: msgs[1].Id},
			response: &types.QueryGetPurchaseResponse{Purchase: msgs[1]},
		},
		{
			desc:    "KeyNotFound",
			request: &types.QueryGetPurchaseRequest{Id: uint64(len(msgs))},
			err:     sdkerrors.ErrKeyNotFound,
		},
		{
			desc: "InvalidRequest",
			err:  status.Error(codes.InvalidArgument, "invalid request"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			response, err := qs.GetPurchase(f.ctx, tc.request)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
				require.EqualExportedValues(t, tc.response, response)
			}
		})
	}
}

func TestPurchaseQueryPaginated(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)
	msgs := createNPurchase(f.keeper, f.ctx, 5)

	request := func(next []byte, offset, limit uint64, total bool) *types.QueryAllPurchaseRequest {
		return &types.QueryAllPurchaseRequest{
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
			resp, err := qs.ListPurchase(f.ctx, request(nil, uint64(i), uint64(step), false))
			require.NoError(t, err)
			require.LessOrEqual(t, len(resp.Purchase), step)
			require.Subset(t, msgs, resp.Purchase)
		}
	})
	t.Run("ByKey", func(t *testing.T) {
		step := 2
		var next []byte
		for i := 0; i < len(msgs); i += step {
			resp, err := qs.ListPurchase(f.ctx, request(next, 0, uint64(step), false))
			require.NoError(t, err)
			require.LessOrEqual(t, len(resp.Purchase), step)
			require.Subset(t, msgs, resp.Purchase)
			next = resp.Pagination.NextKey
		}
	})
	t.Run("Total", func(t *testing.T) {
		resp, err := qs.ListPurchase(f.ctx, request(nil, 0, 0, true))
		require.NoError(t, err)
		require.Equal(t, len(msgs), int(resp.Pagination.Total))
		require.EqualExportedValues(t, msgs, resp.Purchase)
	})
	t.Run("InvalidRequest", func(t *testing.T) {
		_, err := qs.ListPurchase(f.ctx, nil)
		require.ErrorIs(t, err, status.Error(codes.InvalidArgument, "invalid request"))
	})
}
