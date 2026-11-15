package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/mspvantage/smartnodes/x/verify/types"
)

// RevealReport opens a sealed report from the previous round. The values
// sent must hash to exactly what was sealed; anything else is refused, so a
// prober cannot change its answer after seeing the others.
//
// The proof field is stored nowhere and is not checked in this version. The
// proof-of-probe check, which ties a "pass" to a ticket the node redeemed,
// arrives in stage 2.
func (k msgServer) RevealReport(ctx context.Context, msg *types.MsgRevealReport) (*types.MsgRevealReportResponse, error) {
	owner, err := k.addressCodec.StringToBytes(msg.Owner)
	if err != nil {
		return nil, errorsmod.Wrap(err, "invalid owner address")
	}
	ownerStr, err := k.addressCodec.BytesToString(owner)
	if err != nil {
		return nil, err
	}

	if !types.ValidResult(msg.Result) {
		return nil, errorsmod.Wrapf(types.ErrInvalidReport, "result %d, want %d (pass), %d (fail) or %d (tamper)",
			msg.Result, types.ResultPass, types.ResultFail, types.ResultTamper)
	}
	if msg.SpeedBand > types.MaxSpeedBand {
		return nil, errorsmod.Wrapf(types.ErrInvalidReport, "speed band %d, want at most %d", msg.SpeedBand, types.MaxSpeedBand)
	}
	if len(msg.Proof) > types.MaxProofLength {
		return nil, errorsmod.Wrapf(types.ErrInvalidReport, "proof length %d, want at most %d", len(msg.Proof), types.MaxProofLength)
	}
	salt, err := types.DecodeSalt(msg.Salt)
	if err != nil {
		return nil, errorsmod.Wrapf(types.ErrInvalidReport, "salt: %s", err.Error())
	}

	round, _, err := k.CurrentRound(ctx)
	if err != nil {
		return nil, err
	}
	if round == 0 {
		return nil, types.ErrNoRound
	}
	// A round's reports are opened during the following round, and only then.
	if msg.Round+1 != round {
		return nil, errorsmod.Wrapf(types.ErrWrongRound, "got %d, reports being opened now are from round %d", msg.Round, round-1)
	}

	// The prober may have left the active set since sealing; it can still
	// open what it sealed.
	prober, err := k.proberByOwner(ctx, ownerStr)
	if err != nil {
		return nil, err
	}

	key := reportKey(msg.Round, msg.NodeId, prober.Id)
	stored, err := k.Reports.Get(ctx, key)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrapf(types.ErrNoCommit, "node %d, round %d", msg.NodeId, msg.Round)
		}
		return nil, err
	}
	hash, result, _, ok := decodeReport(stored)
	if !ok {
		return nil, errorsmod.Wrap(types.ErrInvalidReport, "stored report is malformed")
	}
	if result != types.ResultSealed {
		return nil, errorsmod.Wrapf(types.ErrAlreadyRevealed, "node %d, round %d", msg.NodeId, msg.Round)
	}

	chainID := sdk.UnwrapSDKContext(ctx).ChainID()
	want := types.CommitHash(chainID, msg.Round, msg.NodeId, prober.Id, msg.Result, msg.SpeedBand, salt)
	if !bytes.Equal(hash, want) {
		return nil, types.ErrRevealMismatch
	}

	if err := k.Reports.Set(ctx, key, encodeReport(hash, msg.Result, msg.SpeedBand)); err != nil {
		return nil, err
	}

	emit(ctx, EventTypeReveal,
		sdk.NewAttribute(AttributeKeyProberID, u64(prober.Id)),
		sdk.NewAttribute(AttributeKeyRound, u64(msg.Round)),
		sdk.NewAttribute(AttributeKeyNodeID, u64(msg.NodeId)),
		sdk.NewAttribute(AttributeKeyResult, u64(msg.Result)),
	)

	return &types.MsgRevealReportResponse{}, nil
}
