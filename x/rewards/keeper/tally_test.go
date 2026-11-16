package keeper_test

import (
	"bytes"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/rewards/keeper"
	"github.com/mspvantage/smartnodes/x/rewards/types"
)

// govWorld is a small chain for vote-counting tests:
//
//	validator one, 1,000 staked:  its operator 100, alice 300, team 600
//	validator two, 1,000 staked:  its operator 400, bob 600
//	validator idle, not in the active set: carol 500
type govWorld struct {
	*fixture
	op1, op2, alice, bob, team, carol sdk.AccAddress
}

func raw(seed byte) sdk.AccAddress { return bytes.Repeat([]byte{seed}, 20) }

func newGovWorld(t *testing.T) *govWorld {
	t.Helper()
	f := initFixture(t)
	w := &govWorld{fixture: f, op1: raw(1), op2: raw(2), alice: raw(3), bob: raw(4), team: raw(5), carol: raw(6)}

	// The mock's validator codec treats an address as its own text, so a
	// validator's operator address is the operator account's raw bytes.
	validator := func(op sdk.AccAddress) stakingtypes.Validator {
		return stakingtypes.Validator{
			OperatorAddress: string(op),
			Status:          stakingtypes.Bonded,
			Tokens:          math.NewInt(1_000),
			DelegatorShares: math.LegacyNewDec(1_000),
		}
	}
	f.staking.bonded = []stakingtypes.Validator{validator(w.op1), validator(w.op2)}

	delegate := func(from, toOperator sdk.AccAddress, shares int64) {
		f.staking.delegations[string(from)] = append(f.staking.delegations[string(from)], stakingtypes.Delegation{
			ValidatorAddress: string(toOperator),
			Shares:           math.LegacyNewDec(shares),
		})
	}
	delegate(w.op1, w.op1, 100)
	delegate(w.alice, w.op1, 300)
	delegate(w.team, w.op1, 600)
	delegate(w.op2, w.op2, 400)
	delegate(w.bob, w.op2, 600)
	delegate(w.carol, raw(9), 500) // a validator outside the active set
	return w
}

func (w *govWorld) text(t *testing.T, addr sdk.AccAddress) string {
	t.Helper()
	s, err := w.addressCodec.BytesToString(addr)
	require.NoError(t, err)
	return s
}

// setNonVoting puts the given accounts on the non-voting list.
func (w *govWorld) setNonVoting(t *testing.T, addrs ...sdk.AccAddress) {
	t.Helper()
	params := types.DefaultParams()
	for _, a := range addrs {
		params.NonVotingAddresses = append(params.NonVotingAddresses, w.text(t, a))
	}
	require.NoError(t, params.Validate())
	require.NoError(t, w.keeper.Params.Set(w.ctx, params))
}

func (w *govWorld) vote(t *testing.T, voter sdk.AccAddress, option v1.VoteOption) v1.Vote {
	t.Helper()
	return v1.Vote{Voter: w.text(t, voter), Options: v1.NewNonSplitVoteOption(option)}
}

type count struct {
	yes, no, abstain, veto int64
	voted, total           int64
}

func (w *govWorld) count(t *testing.T, votes ...v1.Vote) count {
	t.Helper()
	voted, total, results, err := w.keeper.CountVotes(w.ctx, votes)
	require.NoError(t, err)
	whole := func(d math.LegacyDec) int64 {
		require.True(t, d.IsInteger(), "expected a whole number, got %s", d)
		return d.TruncateInt64()
	}
	return count{
		yes:     whole(results[v1.OptionYes]),
		no:      whole(results[v1.OptionNo]),
		abstain: whole(results[v1.OptionAbstain]),
		veto:    whole(results[v1.OptionNoWithVeto]),
		voted:   whole(voted),
		total:   total.Int64(),
	}
}

// With nobody on the list, the count is the standard one.
func TestTallyWithoutNonVotingList(t *testing.T) {
	w := newGovWorld(t)

	// No votes at all.
	require.Equal(t, count{total: 2_000}, w.count(t))

	// Validator one votes yes with everything delegated to it.
	require.Equal(t, count{yes: 1_000, voted: 1_000, total: 2_000},
		w.count(t, w.vote(t, w.op1, v1.OptionYes)))

	// Alice votes no herself: her 300 leave validator one's pile.
	require.Equal(t, count{yes: 700, no: 300, voted: 1_000, total: 2_000},
		w.count(t, w.vote(t, w.op1, v1.OptionYes), w.vote(t, w.alice, v1.OptionNo)))

	// The team, unlisted, votes like anyone else.
	require.Equal(t, count{yes: 400, no: 600, voted: 1_000, total: 2_000},
		w.count(t, w.vote(t, w.op1, v1.OptionYes), w.vote(t, w.team, v1.OptionNo)))

	// A delegator votes but their validator does not: only the delegator counts.
	require.Equal(t, count{veto: 600, voted: 600, total: 2_000},
		w.count(t, w.vote(t, w.bob, v1.OptionNoWithVeto)))

	// Both validators and every delegator vote.
	require.Equal(t, count{yes: 100 + 300 + 600, no: 400, abstain: 600, voted: 2_000, total: 2_000},
		w.count(t,
			w.vote(t, w.op1, v1.OptionYes), w.vote(t, w.alice, v1.OptionYes), w.vote(t, w.team, v1.OptionYes),
			w.vote(t, w.op2, v1.OptionNo), w.vote(t, w.bob, v1.OptionAbstain)))
}

// Stake from a listed account has no say: the validator does not vote with
// it, and it is left out of the quorum total.
func TestNonVotingStakeIsNotInherited(t *testing.T) {
	w := newGovWorld(t)
	w.setNonVoting(t, w.team)

	// Validator one votes yes. It no longer carries the team's 600.
	require.Equal(t, count{yes: 400, voted: 400, total: 1_400},
		w.count(t, w.vote(t, w.op1, v1.OptionYes)))

	// With no votes, the quorum total is still 1,400.
	require.Equal(t, count{total: 1_400}, w.count(t))

	// The other validator is unaffected.
	require.Equal(t, count{no: 1_000, voted: 1_000, total: 1_400},
		w.count(t, w.vote(t, w.op2, v1.OptionNo)))
}

// A vote sent by a listed account is ignored.
func TestNonVotingAccountCannotVote(t *testing.T) {
	w := newGovWorld(t)
	w.setNonVoting(t, w.team)

	require.Equal(t, count{total: 1_400}, w.count(t, w.vote(t, w.team, v1.OptionNo)))

	// The team's vote changes nothing next to other votes either.
	with := w.count(t, w.vote(t, w.op1, v1.OptionYes), w.vote(t, w.alice, v1.OptionNo), w.vote(t, w.team, v1.OptionNoWithVeto))
	without := w.count(t, w.vote(t, w.op1, v1.OptionYes), w.vote(t, w.alice, v1.OptionNo))
	require.Equal(t, without, with)
	require.Equal(t, count{yes: 100, no: 300, voted: 400, total: 1_400}, with)
}

// The same five votes, counted with and without the team on the list. Listed,
// the team's 600 drops out of the result and out of the quorum total, and
// nothing else changes.
func TestListingRemovesExactlyTheListedStake(t *testing.T) {
	votes := func(w *govWorld) []v1.Vote {
		return []v1.Vote{
			w.vote(t, w.op1, v1.OptionYes),  // the validator the team delegates to
			w.vote(t, w.team, v1.OptionYes), // the team itself
			w.vote(t, w.alice, v1.OptionNo),
			w.vote(t, w.op2, v1.OptionNo),
			w.vote(t, w.bob, v1.OptionNo),
		}
	}

	open := newGovWorld(t)
	require.Equal(t, count{yes: 700, no: 1_300, voted: 2_000, total: 2_000}, open.count(t, votes(open)...))

	listed := newGovWorld(t)
	listed.setNonVoting(t, listed.team)
	require.Equal(t, count{yes: 100, no: 1_300, voted: 1_400, total: 1_400}, listed.count(t, votes(listed)...))
}

// The case the rule exists for: a validator holding a large insider
// delegation votes yes and nobody delegated to it objects. Unlisted, that
// delegation carries the vote; listed, it does not.
func TestInsiderDelegationCannotCarryAVote(t *testing.T) {
	votes := func(w *govWorld) []v1.Vote {
		return []v1.Vote{
			w.vote(t, w.op1, v1.OptionYes), // carries alice's 300 and the team's 600
			w.vote(t, w.bob, v1.OptionNo),
		}
	}

	open := newGovWorld(t)
	got := open.count(t, votes(open)...)
	require.Equal(t, count{yes: 1_000, no: 600, voted: 1_600, total: 2_000}, got)
	require.Greater(t, got.yes, got.no, "unlisted, the insider delegation wins the vote")

	listed := newGovWorld(t)
	listed.setNonVoting(t, listed.team)
	got = listed.count(t, votes(listed)...)
	require.Equal(t, count{yes: 400, no: 600, voted: 1_000, total: 1_400}, got)
	require.Less(t, got.yes, got.no, "listed, it does not")
}

// A listed account that operates a validator cannot vote as the validator.
func TestNonVotingOperatorCannotVoteAsValidator(t *testing.T) {
	w := newGovWorld(t)
	w.setNonVoting(t, w.op2)

	// Validator two's operator is listed: its own 400 is excluded, and its
	// vote does not carry bob's 600 either.
	require.Equal(t, count{total: 1_600}, w.count(t, w.vote(t, w.op2, v1.OptionYes)))
	// Bob can still vote for himself.
	require.Equal(t, count{no: 600, voted: 600, total: 1_600},
		w.count(t, w.vote(t, w.op2, v1.OptionYes), w.vote(t, w.bob, v1.OptionNo)))
}

// Stake delegated to a validator outside the active set never counts, listed
// or not, and listing an account with no stake changes nothing.
func TestTallyIgnoresInactiveValidators(t *testing.T) {
	w := newGovWorld(t)
	require.Equal(t, count{total: 2_000}, w.count(t, w.vote(t, w.carol, v1.OptionYes)))

	w.setNonVoting(t, w.carol, raw(0x77))
	require.Equal(t, count{yes: 1_000, voted: 1_000, total: 2_000},
		w.count(t, w.vote(t, w.op1, v1.OptionYes)))
}

// A split vote is divided by its weights, as in the standard count.
func TestTallySplitVote(t *testing.T) {
	w := newGovWorld(t)
	w.setNonVoting(t, w.team)
	split := v1.Vote{Voter: w.text(t, w.bob), Options: v1.WeightedVoteOptions{
		{Option: v1.OptionYes, Weight: "0.5"},
		{Option: v1.OptionNo, Weight: "0.5"},
	}}
	require.Equal(t, count{yes: 300, no: 300, voted: 600, total: 1_400}, w.count(t, split))
}

func TestTallyRejectsUnreadableVoter(t *testing.T) {
	w := newGovWorld(t)
	_, _, _, err := w.keeper.CountVotes(w.ctx, []v1.Vote{{Voter: "not-an-address", Options: v1.NewNonSplitVoteOption(v1.OptionYes)}})
	require.Error(t, err)
}

func TestIsNonVoting(t *testing.T) {
	w := newGovWorld(t)
	w.setNonVoting(t, w.team)
	yes, err := w.keeper.IsNonVoting(w.ctx, w.team)
	require.NoError(t, err)
	require.True(t, yes)
	no, err := w.keeper.IsNonVoting(w.ctx, w.alice)
	require.NoError(t, err)
	require.False(t, no)
}

// Governance may take addresses off the list but never add one.
func TestNonVotingListOnlyShrinks(t *testing.T) {
	w := newGovWorld(t)
	w.setNonVoting(t, w.team, w.carol)
	ms := keeper.NewMsgServerImpl(w.keeper)
	authority, err := w.addressCodec.BytesToString(w.keeper.GetAuthority())
	require.NoError(t, err)

	update := func(addrs ...sdk.AccAddress) error {
		params := types.DefaultParams()
		for _, a := range addrs {
			params.NonVotingAddresses = append(params.NonVotingAddresses, w.text(t, a))
		}
		_, err := ms.UpdateParams(w.ctx, &types.MsgUpdateParams{Authority: authority, Params: params})
		return err
	}

	require.ErrorIs(t, update(w.team, w.carol, w.alice), types.ErrNonVotingListGrew, "adding an address")
	require.ErrorIs(t, update(w.alice), types.ErrNonVotingListGrew, "swapping in a new address")
	require.NoError(t, update(w.carol, w.team), "the same list in another order")
	require.NoError(t, update(w.team), "removing one address")

	// Once removed, an address cannot be put back.
	require.ErrorIs(t, update(w.team, w.carol), types.ErrNonVotingListGrew)
	require.NoError(t, update(), "removing the last address")
	require.ErrorIs(t, update(w.team), types.ErrNonVotingListGrew)

	// Other settings can still be changed while the list is untouched.
	params := types.DefaultParams()
	params.MaxCommissionBps = 1_500
	_, err = ms.UpdateParams(w.ctx, &types.MsgUpdateParams{Authority: authority, Params: params})
	require.NoError(t, err)
}

func TestNonVotingParamValidation(t *testing.T) {
	p := types.DefaultParams()
	require.Empty(t, p.NonVotingAddresses)

	p.NonVotingAddresses = []string{"a", "b"}
	require.NoError(t, p.Validate())
	p.NonVotingAddresses = []string{"a", "a"}
	require.ErrorIs(t, p.Validate(), types.ErrInvalidParams)
	p.NonVotingAddresses = []string{"a", ""}
	require.ErrorIs(t, p.Validate(), types.ErrInvalidParams)

	p.NonVotingAddresses = nil
	for i := 0; i <= types.MaxNonVotingAddresses; i++ {
		p.NonVotingAddresses = append(p.NonVotingAddresses, string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	require.ErrorIs(t, p.Validate(), types.ErrInvalidParams)

	require.True(t, types.NonVotingOnlyShrinks([]string{"a", "b"}, []string{"b"}))
	require.True(t, types.NonVotingOnlyShrinks([]string{"a", "b"}, nil))
	require.True(t, types.NonVotingOnlyShrinks(nil, nil))
	require.False(t, types.NonVotingOnlyShrinks([]string{"a"}, []string{"a", "c"}))
	require.False(t, types.NonVotingOnlyShrinks(nil, []string{"a"}))
}

// A genesis file with an unreadable non-voting address must not start the
// chain: a typing mistake would otherwise leave tokens voting silently.
func TestGenesisRejectsUnreadableNonVotingAddress(t *testing.T) {
	w := newGovWorld(t)
	gen := types.DefaultGenesis()
	gen.Params.NonVotingAddresses = []string{w.text(t, w.team), "smart1typo"}
	require.ErrorIs(t, w.keeper.InitGenesis(w.ctx, *gen), types.ErrInvalidParams)

	gen.Params.NonVotingAddresses = []string{w.text(t, w.team)}
	require.NoError(t, w.keeper.InitGenesis(w.ctx, *gen))
	yes, err := w.keeper.IsNonVoting(w.ctx, w.team)
	require.NoError(t, err)
	require.True(t, yes)
}
