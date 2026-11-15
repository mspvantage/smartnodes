package keeper

import (
	"context"
	"encoding/hex"
	"errors"

	"cosmossdk.io/collections"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// Round returns the current round, its seed and when the next one starts.
// Prober software uses the seed to work out its own assignments.
func (q queryServer) Round(ctx context.Context, req *types.QueryRoundRequest) (*types.QueryRoundResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	round, start, err := q.k.CurrentRound(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	active, err := q.k.activeCount(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res := &types.QueryRoundResponse{Round: round, StartHeight: start, ActiveProbers: active}
	if round == 0 {
		return res, nil
	}

	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res.NextRoundHeight = start + int64(params.RoundBlocks)

	seed, err := q.k.RoundSeed.Get(ctx, round)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res.Seed = hex.EncodeToString(seed)
	return res, nil
}

// NodeRecord returns a node's verification record.
func (q queryServer) NodeRecord(ctx context.Context, req *types.QueryNodeRecordRequest) (*types.QueryNodeRecordResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	rec, err := q.k.NodeRecord.Get(ctx, req.NodeId)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, sdkerrors.ErrKeyNotFound
		}
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &types.QueryNodeRecordResponse{NodeRecord: rec}, nil
}

// Assignment returns whether a node is probed in the current round and, if
// so, which probers are assigned to it.
func (q queryServer) Assignment(ctx context.Context, req *types.QueryAssignmentRequest) (*types.QueryAssignmentResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	round, _, err := q.k.CurrentRound(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res := &types.QueryAssignmentResponse{Round: round}
	if round == 0 {
		return res, nil
	}

	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	seed, err := q.k.RoundSeed.Get(ctx, round)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !types.Selected(seed, req.NodeId, params.SampleBps) {
		return res, nil
	}
	res.Selected = true

	active, err := q.k.activeCount(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	for _, idx := range types.AssignedIndices(seed, req.NodeId, active, params.ProbersPerNode) {
		id, err := q.k.ActiveList.Get(ctx, idx)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		res.ProberIds = append(res.ProberIds, id)
	}
	return res, nil
}
