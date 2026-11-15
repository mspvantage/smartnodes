package keeper

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

const (
	// MaxSetChangesPerRound bounds how many probers join, and how many
	// leave, the active set when a round starts.
	MaxSetChangesPerRound = 500
	// MaxTallyReportsPerBlock bounds the reports tallied in one block. A
	// round with more reports is tallied over several blocks.
	MaxTallyReportsPerBlock = 300
	// maxTallyOverrun lets the tally finish the node it is on after the
	// bound is reached, so a node's reports are normally counted together.
	maxTallyOverrun = 64
	// SeedsKept is how many recent round seeds stay in the store.
	SeedsKept = 3
	// GasPerCommit is charged for each sealed report: one registry read, one
	// assignment check and one write.
	GasPerCommit = 5_000

	reportValueLength = types.HashLength + 2
)

// reportKey builds the store key of a report.
func reportKey(round, nodeID, proberID uint64) collections.Pair[uint64, []byte] {
	suffix := make([]byte, 16)
	binary.BigEndian.PutUint64(suffix[:8], nodeID)
	binary.BigEndian.PutUint64(suffix[8:], proberID)
	return collections.Join(round, suffix)
}

// splitReportKey returns the node and prober of a report key.
func splitReportKey(key collections.Pair[uint64, []byte]) (nodeID, proberID uint64, ok bool) {
	suffix := key.K2()
	if len(suffix) != 16 {
		return 0, 0, false
	}
	return binary.BigEndian.Uint64(suffix[:8]), binary.BigEndian.Uint64(suffix[8:]), true
}

// A stored report is the sealed hash, then one byte for the opened result
// (0 while still sealed), then one byte for the speed band.
func encodeReport(hash []byte, result, speedBand uint64) []byte {
	out := make([]byte, 0, reportValueLength)
	out = append(out, hash...)
	return append(out, byte(result), byte(speedBand))
}

func decodeReport(value []byte) (hash []byte, result, speedBand uint64, ok bool) {
	if len(value) != reportValueLength {
		return nil, 0, 0, false
	}
	return value[:types.HashLength], uint64(value[types.HashLength]), uint64(value[types.HashLength+1]), true
}

// CurrentRound returns the current round number and the height it started
// at. The round is 0 before the first block has been processed.
func (k Keeper) CurrentRound(ctx context.Context) (round uint64, start int64, err error) {
	round, err = k.RoundNumber.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	start, err = k.RoundStart.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return round, 0, nil
		}
		return 0, 0, err
	}
	return round, start, nil
}

// BeginBlocker starts a new round when one is due and tallies finished
// rounds, a bounded number of reports per block.
//
// The work runs on a copy of the state. If anything fails, the copy is
// discarded, an event records why, and the block carries on. A fault here
// must never stop the chain.
func (k Keeper) BeginBlocker(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.runBlock(cacheCtx); err != nil {
		sdkCtx.Logger().Error("verify: begin block skipped", "error", err.Error())
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(EventTypeBlockerFailed,
			sdk.NewAttribute(AttributeKeyReason, err.Error()),
		))
		return nil
	}
	write()
	return nil
}

func (k Keeper) runBlock(ctx sdk.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	round, start, err := k.CurrentRound(ctx)
	if err != nil {
		return err
	}

	height := ctx.BlockHeight()
	// Rounds are counted, not derived from the height, so changing the round
	// length never makes the round number jump or go backwards.
	if round == 0 || height >= start+int64(params.RoundBlocks) {
		round++
		if err := k.startRound(ctx, round); err != nil {
			return err
		}
	}

	return k.TallyDue(ctx, round, params)
}

// startRound fixes the seed and the active prober set for a new round.
func (k Keeper) startRound(ctx sdk.Context, round uint64) error {
	var prev []byte
	if round > 1 {
		seed, err := k.RoundSeed.Get(ctx, round-1)
		if err != nil && !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		prev = seed
	}
	seed := types.NextSeed(prev, round, ctx.HeaderHash())
	if err := k.RoundSeed.Set(ctx, round, seed); err != nil {
		return err
	}
	if round > SeedsKept {
		if err := k.RoundSeed.Remove(ctx, round-SeedsKept); err != nil {
			return err
		}
	}
	if err := k.RoundNumber.Set(ctx, round); err != nil {
		return err
	}
	if err := k.RoundStart.Set(ctx, ctx.BlockHeight()); err != nil {
		return err
	}

	if err := k.applySetChanges(ctx); err != nil {
		return err
	}
	active, err := k.activeCount(ctx)
	if err != nil {
		return err
	}

	emit(ctx, EventTypeNewRound,
		sdk.NewAttribute(AttributeKeyRound, u64(round)),
		sdk.NewAttribute(AttributeKeySeed, hex.EncodeToString(seed)),
		sdk.NewAttribute(AttributeKeyActive, u64(active)),
	)
	return nil
}

// tallyEntry is one stored report read back for the tally.
type tallyEntry struct {
	key      collections.Pair[uint64, []byte]
	nodeID   uint64
	proberID uint64
	result   uint64
}

// TallyDue counts the reports of rounds whose reveal window has closed.
// Reports of round r are sealed during r and opened during r+1, so they are
// counted from round r+2 on. Counted reports are deleted.
//
// Reports are stored in order of round and then node, so the oldest round is
// always first and one node's reports are read together.
func (k Keeper) TallyDue(ctx context.Context, current uint64, params types.Params) error {
	if current < 3 {
		return nil
	}
	lastDue := current - 2

	iter, err := k.Reports.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	var entries []tallyEntry
	for ; iter.Valid(); iter.Next() {
		kv, err := iter.KeyValue()
		if err != nil {
			_ = iter.Close()
			return err
		}
		round := kv.Key.K1()
		if round > lastDue {
			break
		}
		nodeID, proberID, ok := splitReportKey(kv.Key)
		_, result, _, okValue := decodeReport(kv.Value)
		if !ok || !okValue {
			// A malformed entry cannot be counted. Drop it below, as a
			// sealed report from prober 0 for node 0, so it cannot block
			// the queue.
			result = types.ResultSealed
		}
		if n := len(entries); n >= MaxTallyReportsPerBlock {
			last := entries[n-1]
			sameNode := last.key.K1() == round && last.nodeID == nodeID
			if !sameNode || n >= MaxTallyReportsPerBlock+maxTallyOverrun {
				break
			}
		}
		entries = append(entries, tallyEntry{key: kv.Key, nodeID: nodeID, proberID: proberID, result: result})
	}
	if err := iter.Close(); err != nil {
		return err
	}

	for from := 0; from < len(entries); {
		to := from + 1
		for to < len(entries) &&
			entries[to].key.K1() == entries[from].key.K1() &&
			entries[to].nodeID == entries[from].nodeID {
			to++
		}
		if err := k.tallyNode(ctx, entries[from:to], params); err != nil {
			return err
		}
		from = to
	}
	return nil
}

// tallyNode decides one node's outcome for one round, updates the node's
// record and the probers' counts, and deletes the reports.
func (k Keeper) tallyNode(ctx context.Context, group []tallyEntry, params types.Params) error {
	round := group[0].key.K1()
	nodeID := group[0].nodeID

	var pass, fail, tamper, silent uint64
	for _, e := range group {
		switch e.result {
		case types.ResultPass:
			pass++
		case types.ResultFail:
			fail++
		case types.ResultTamper:
			tamper++
		default:
			silent++
		}
	}
	outcome, tamperProven := types.Tally(pass, fail, tamper, params.ProbersPerNode)

	for _, e := range group {
		if err := k.Reports.Remove(ctx, e.key); err != nil {
			return err
		}
		if err := k.scoreProber(ctx, e.proberID, e.result, outcome); err != nil {
			return err
		}
	}

	// A node that has left the registry keeps no record.
	if _, err := k.registryKeeper.GetNode(ctx, nodeID); err != nil {
		if err := k.NodeRecord.Remove(ctx, nodeID); err != nil {
			return err
		}
		return nil
	}

	rec, err := k.NodeRecord.Get(ctx, nodeID)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		rec = types.NodeRecord{NodeId: nodeID}
	}
	rec.LastRound = round
	switch outcome {
	case types.OutcomePass:
		rec.Verified = true
		rec.LastPassRound = round
		rec.ConsecutiveFails = 0
		rec.Passes++
	case types.OutcomeFail:
		rec.Verified = false
		rec.ConsecutiveFails++
		rec.Fails++
	default:
		// Not enough agreeing reports to decide. The node is not verified
		// for this round, but this is not counted against it.
		rec.Verified = false
	}
	if tamperProven {
		rec.TamperRounds++
	}
	if err := k.NodeRecord.Set(ctx, nodeID, rec); err != nil {
		return err
	}

	emit(ctx, EventTypeTally,
		sdk.NewAttribute(AttributeKeyRound, u64(round)),
		sdk.NewAttribute(AttributeKeyNodeID, u64(nodeID)),
		sdk.NewAttribute(AttributeKeyOutcome, outcome),
		sdk.NewAttribute(AttributeKeyPass, u64(pass)),
		sdk.NewAttribute(AttributeKeyFail, u64(fail)),
		sdk.NewAttribute(AttributeKeyTamper, u64(tamper)),
		sdk.NewAttribute(AttributeKeySilent, u64(silent)),
	)
	// This version only reports. The event marks where jailing would happen
	// once penalties are switched on.
	if outcome == types.OutcomeFail && rec.ConsecutiveFails >= params.FailsBeforeJail {
		emit(ctx, EventTypeWouldJail,
			sdk.NewAttribute(AttributeKeyNodeID, u64(nodeID)),
			sdk.NewAttribute(AttributeKeyFails, u64(rec.ConsecutiveFails)),
		)
	}
	return nil
}

// scoreProber records whether a prober's report agreed with the outcome.
//
//   - A sealed report that was never opened counts against the prober.
//   - If the node's outcome was decided, an opened report counts for the
//     prober when it agrees and against it when it does not.
//   - If the outcome was not decided, opened reports are not scored.
func (k Keeper) scoreProber(ctx context.Context, proberID, result uint64, outcome string) error {
	var accepted, outlier bool
	switch {
	case !types.ValidResult(result):
		outlier = true
	case outcome == types.OutcomePass:
		accepted = result == types.ResultPass
		outlier = !accepted
	case outcome == types.OutcomeFail:
		accepted = result == types.ResultFail || result == types.ResultTamper
		outlier = !accepted
	}
	if !accepted && !outlier {
		return nil
	}

	prober, err := k.Prober.Get(ctx, proberID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	if accepted {
		prober.Accepted++
	} else {
		prober.Outliers++
	}
	return k.Prober.Set(ctx, proberID, prober)
}
