package types_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/registry/types"
)

func TestValidateEndpoint(t *testing.T) {
	valid := []string{
		"node.example.com:51820",
		"198.51.100.7:443",
		"[2001:db8::1]:51820",
		"a:1",
		"host:65535",
	}
	for _, e := range valid {
		require.NoError(t, types.ValidateEndpoint(e), e)
	}

	invalid := []string{
		"",
		"node.example.com",
		":51820",
		"host:0",
		"host:65536",
		"host:-1",
		"host:port",
		"host:51820 ",
		"ho st:51820",
		"host:51820\n",
		"2001:db8::1:51820",
		"hôst:51820",
		strings.Repeat("a", types.MaxEndpointLength) + ":1",
	}
	for _, e := range invalid {
		require.ErrorIs(t, types.ValidateEndpoint(e), types.ErrInvalidEndpoint, "%q", e)
	}
}

func TestValidatePubkey(t *testing.T) {
	require.NoError(t, types.ValidatePubkey(testPubkey(0)))
	require.NoError(t, types.ValidatePubkey(testPubkey(255)))

	good := testPubkey(7)
	invalid := []string{
		"",
		"AAAA",                             // 3 bytes
		good[:len(good)-1],                 // truncated
		good + "AAAA",                      // 35 bytes
		strings.TrimRight(good, "="),       // padding removed
		strings.ReplaceAll(good, "B", "-"), // not the standard alphabet
		" " + good,
	}
	for _, k := range invalid {
		require.ErrorIs(t, types.ValidatePubkey(k), types.ErrInvalidPubkey, "%q", k)
	}
}

func TestValidateTier(t *testing.T) {
	require.NoError(t, types.ValidateTier(types.TierEntry))
	require.NoError(t, types.ValidateTier(types.TierExit))
	require.ErrorIs(t, types.ValidateTier(0), types.ErrInvalidTier)
	require.ErrorIs(t, types.ValidateTier(3), types.ErrInvalidTier)
}

func TestParamsValidate(t *testing.T) {
	require.NoError(t, types.DefaultParams().Validate())
	require.NoError(t, types.NewParams(1, 1, types.MinUnbondingSeconds).Validate())
	require.NoError(t, types.NewParams(1, 1, types.MaxUnbondingSeconds).Validate())

	require.ErrorIs(t, types.NewParams(0, 1, types.MinUnbondingSeconds).Validate(), types.ErrInvalidParams)
	require.ErrorIs(t, types.NewParams(1, 0, types.MinUnbondingSeconds).Validate(), types.ErrInvalidParams)
	require.ErrorIs(t, types.NewParams(1, 1, types.MinUnbondingSeconds-1).Validate(), types.ErrInvalidParams)
	require.ErrorIs(t, types.NewParams(1, 1, types.MaxUnbondingSeconds+1).Validate(), types.ErrInvalidParams)
	require.ErrorIs(t, types.Params{}.Validate(), types.ErrInvalidParams)

	minBond, err := types.DefaultParams().MinBond(types.TierExit)
	require.NoError(t, err)
	require.Equal(t, types.DefaultMinBondExit, minBond)
	_, err = types.DefaultParams().MinBond(9)
	require.ErrorIs(t, err, types.ErrInvalidTier)
}
