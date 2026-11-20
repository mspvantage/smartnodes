package keeper

import (
	"context"
	"encoding/hex"
	"errors"

	"cosmossdk.io/collections"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
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
	// A jailed node is not probed until its operator unjails it.
	jailed, err := q.k.isJailed(ctx, req.NodeId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if jailed || !types.Selected(seed, req.NodeId, params.SampleBps) {
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

// ProberAssignments lists the nodes a prober must probe in the current
// round: every active, unjailed, selected node whose assignment includes it.
// It reads every node, which costs the queried server, not the chain, and
// saves the prober asking about each node in turn.
func (q queryServer) ProberAssignments(ctx context.Context, req *types.QueryProberAssignmentsRequest) (*types.QueryProberAssignmentsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	round, _, err := q.k.CurrentRound(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res := &types.QueryProberAssignmentsResponse{Round: round}
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
	active, err := q.k.activeCount(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// The prober's places in the active list; usually one.
	mine := map[uint64]bool{}
	for idx := uint64(0); idx < active; idx++ {
		id, err := q.k.ActiveList.Get(ctx, idx)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if id == req.ProberId {
			mine[idx] = true
		}
	}
	if len(mine) == 0 {
		return res, nil
	}
	err = q.k.registryKeeper.WalkNodes(ctx, func(n registrytypes.Node) (bool, error) {
		if n.Status != registrytypes.StatusActive || !types.Selected(seed, n.Id, params.SampleBps) {
			return false, nil
		}
		jailed, err := q.k.isJailed(ctx, n.Id)
		if err != nil || jailed {
			return false, err
		}
		for _, idx := range types.AssignedIndices(seed, n.Id, active, params.ProbersPerNode) {
			if mine[idx] {
				res.NodeIds = append(res.NodeIds, n.Id)
				break
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return res, nil
}

// RewardPool returns what the node rewards pool holds, what it owes to nodes
// that have not claimed yet, and the current round's budget.
func (q queryServer) RewardPool(ctx context.Context, req *types.QueryRewardPoolRequest) (*types.QueryRewardPoolResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}

	balance, owed, _, _, err := q.k.PoolState(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	address, err := q.k.addressCodec.BytesToString(PoolAddress())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res := &types.QueryRewardPoolResponse{Address: address}
	if balance.IsUint64() {
		res.Balance = balance.Uint64()
	}
	if owed.IsUint64() {
		res.Owed = owed.Uint64()
	}

	round, _, err := q.k.CurrentRound(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	budget, err := q.k.RoundBudget.Get(ctx, round)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res.RoundBudget = budget
	return res, nil
}
