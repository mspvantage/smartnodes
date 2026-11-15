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

	"github.com/mspvantage/smartnodes/x/registry/keeper"
	"github.com/mspvantage/smartnodes/x/registry/types"
)

func createNNode(keeper keeper.Keeper, ctx context.Context, n int) []types.Node {
	items := make([]types.Node, n)
	for i := range items {
		iu := uint64(i)
		items[i].Id = iu
		items[i].Operator = fmt.Sprintf(`cosmos1abcdef%d`, i)
		items[i].Endpoint = strconv.Itoa(i)
		items[i].Pubkey = strconv.Itoa(i)
		items[i].Tier = uint64(i)
		items[i].Bond = sdk.NewInt64Coin(`token`, int64(i+100))
		items[i].Status = uint64(i)
		items[i].UnbondingEnd = int64(i)
		_ = keeper.Node.Set(ctx, iu, items[i])
		_ = keeper.NodeSeq.Set(ctx, iu)
	}
	return items
}

func TestNodeQuerySingle(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)
	msgs := createNNode(f.keeper, f.ctx, 2)
	tests := []struct {
		desc     string
		request  *types.QueryGetNodeRequest
		response *types.QueryGetNodeResponse
		err      error
	}{
		{
			desc:     "First",
			request:  &types.QueryGetNodeRequest{Id: msgs[0].Id},
			response: &types.QueryGetNodeResponse{Node: msgs[0]},
		},
		{
			desc:     "Second",
			request:  &types.QueryGetNodeRequest{Id: msgs[1].Id},
			response: &types.QueryGetNodeResponse{Node: msgs[1]},
		},
		{
			desc:    "KeyNotFound",
			request: &types.QueryGetNodeRequest{Id: uint64(len(msgs))},
			err:     sdkerrors.ErrKeyNotFound,
		},
		{
			desc: "InvalidRequest",
			err:  status.Error(codes.InvalidArgument, "invalid request"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			response, err := qs.GetNode(f.ctx, tc.request)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
				require.EqualExportedValues(t, tc.response, response)
			}
		})
	}
}

func TestNodeQueryPaginated(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)
	msgs := createNNode(f.keeper, f.ctx, 5)

	request := func(next []byte, offset, limit uint64, total bool) *types.QueryAllNodeRequest {
		return &types.QueryAllNodeRequest{
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
			resp, err := qs.ListNode(f.ctx, request(nil, uint64(i), uint64(step), false))
			require.NoError(t, err)
			require.LessOrEqual(t, len(resp.Node), step)
			require.Subset(t, msgs, resp.Node)
		}
	})
	t.Run("ByKey", func(t *testing.T) {
		step := 2
		var next []byte
		for i := 0; i < len(msgs); i += step {
			resp, err := qs.ListNode(f.ctx, request(next, 0, uint64(step), false))
			require.NoError(t, err)
			require.LessOrEqual(t, len(resp.Node), step)
			require.Subset(t, msgs, resp.Node)
			next = resp.Pagination.NextKey
		}
	})
	t.Run("Total", func(t *testing.T) {
		resp, err := qs.ListNode(f.ctx, request(nil, 0, 0, true))
		require.NoError(t, err)
		require.Equal(t, len(msgs), int(resp.Pagination.Total))
		require.EqualExportedValues(t, msgs, resp.Node)
	})
	t.Run("InvalidRequest", func(t *testing.T) {
		_, err := qs.ListNode(f.ctx, nil)
		require.ErrorIs(t, err, status.Error(codes.InvalidArgument, "invalid request"))
	})
}
