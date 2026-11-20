package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	registrytypes "github.com/mspvantage/smartnodes/x/registry/types"
	"github.com/mspvantage/smartnodes/x/verify/types"
)

// CommitReports posts sealed reports for the current round. Each one is a
// hash that fixes the prober's result without showing it, so no prober can
// wait and copy another's answer.
//
// A sealed report is accepted only from a prober in the active set, for an
// active node that is probed this round, to which the prober is assigned,
// and only once.
func (k msgServer) CommitReports(ctx context.Context, msg *types.MsgCommitReports) (*types.MsgCommitReportsResponse, error) {
	owner, err := k.addressCodec.StringToBytes(msg.Owner)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid owner address")
	}
	ownerStr, err := k.addressCodec.BytesToString(owner)
	if err != nil {
		return nil, err
	}

	if len(msg.NodeIds) == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidReport, "no reports")
	}
	if len(msg.NodeIds) > types.MaxCommitsPerMsg {
		return nil, errorsmod.Wrapf(types.ErrInvalidReport, "%d reports, at most %d per message", len(msg.NodeIds), types.MaxCommitsPerMsg)
	}
	if len(msg.NodeIds) != len(msg.Hashes) {
		return nil, errorsmod.Wrapf(types.ErrInvalidReport, "%d node ids but %d hashes", len(msg.NodeIds), len(msg.Hashes))
	}

	round, _, err := k.CurrentRound(ctx)
	if err != nil {
		return nil, err
	}
	if round == 0 {
		return nil, types.ErrNoRound
	}
	if msg.Round != round {
		return nil, errorsmod.Wrapf(types.ErrWrongRound, "got %d, current round is %d", msg.Round, round)
	}

	prober, err := k.proberByOwner(ctx, ownerStr)
	if err != nil {
		return nil, err
	}
	idx, err := k.ActiveIndex.Get(ctx, prober.Id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrapf(types.ErrProberNotActive, "prober %d", prober.Id)
		}
		return nil, err
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	seed, err := k.RoundSeed.Get(ctx, round)
	if err != nil {
		return nil, err
	}
	active, err := k.activeCount(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[uint64]bool, len(msg.NodeIds))
	for i, nodeID := range msg.NodeIds {
		consumeGas(ctx, GasPerCommit, "verify: sealed report")

		if seen[nodeID] {
			return nil, errorsmod.Wrapf(types.ErrInvalidReport, "node %d appears twice", nodeID)
		}
		seen[nodeID] = true

		hash, err := types.DecodeHash(msg.Hashes[i])
		if err != nil {
			return nil, errorsmod.Wrapf(types.ErrInvalidReport, "hash for node %d: %s", nodeID, err.Error())
		}

		node, err := k.registryKeeper.GetNode(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		if node.Status != registrytypes.StatusActive {
			return nil, errorsmod.Wrapf(types.ErrNodeNotActive, "node %d", nodeID)
		}
		if node.Commitment != "" {
			// Bridges are never assigned, so no report on one is accepted.
			return nil, errorsmod.Wrapf(types.ErrNodeNotSelected, "node %d is a bridge", nodeID)
		}
		jailed, err := k.isJailed(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		if jailed {
			return nil, errorsmod.Wrapf(types.ErrNodeJailed, "node %d", nodeID)
		}
		bps, err := k.aimedBps(ctx, params, round, nodeID)
		if err != nil {
			return nil, err
		}
		if !types.Selected(seed, nodeID, bps) {
			return nil, errorsmod.Wrapf(types.ErrNodeNotSelected, "node %d, round %d", nodeID, round)
		}
		if !types.IsAssigned(seed, nodeID, active, params.ProbersPerNode, idx) {
			return nil, errorsmod.Wrapf(types.ErrNotAssigned, "prober %d, node %d, round %d", prober.Id, nodeID, round)
		}

		key := reportKey(round, nodeID, prober.Id)
		exists, err := k.Reports.Has(ctx, key)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, errorsmod.Wrapf(types.ErrAlreadyCommitted, "node %d, round %d", nodeID, round)
		}
		if err := k.Reports.Set(ctx, key, encodeReport(hash, types.ResultSealed, 0)); err != nil {
			return nil, err
		}
	}

	emit(ctx, EventTypeCommit,
		sdk.NewAttribute(AttributeKeyProberID, u64(prober.Id)),
		sdk.NewAttribute(AttributeKeyRound, u64(round)),
		sdk.NewAttribute(AttributeKeyCount, u64(uint64(len(msg.NodeIds)))),
	)

	return &types.MsgCommitReportsResponse{}, nil
}
