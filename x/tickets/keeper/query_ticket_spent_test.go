package keeper_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	"github.com/mspvantage/smartnodes/x/tickets/keeper"
	"github.com/mspvantage/smartnodes/x/tickets/types"
)

func TestTicketSpentQuery(t *testing.T) {
	f := initFixture(t)
	qs := keeper.NewQueryServerImpl(f.keeper)

	spentKey := bytes.Repeat([]byte{7}, 32)
	require.NoError(t, f.keeper.Spent.Set(f.ctx, collections.Join(uint64(3), spentKey)))

	res, err := qs.TicketSpent(f.ctx, &types.QueryTicketSpentRequest{KeyId: 3, TicketKey: hex.EncodeToString(spentKey)})
	require.NoError(t, err)
	require.True(t, res.Spent)

	// The same ticket key under another issuer key is a different ticket.
	res, err = qs.TicketSpent(f.ctx, &types.QueryTicketSpentRequest{KeyId: 4, TicketKey: hex.EncodeToString(spentKey)})
	require.NoError(t, err)
	require.False(t, res.Spent)

	res, err = qs.TicketSpent(f.ctx, &types.QueryTicketSpentRequest{KeyId: 3, TicketKey: hex.EncodeToString(bytes.Repeat([]byte{8}, 32))})
	require.NoError(t, err)
	require.False(t, res.Spent)

	for name, bad := range map[string]string{
		"empty":   "",
		"short":   strings.Repeat("ab", 31),
		"long":    strings.Repeat("ab", 33),
		"not hex": strings.Repeat("zz", 32),
	} {
		_, err := qs.TicketSpent(f.ctx, &types.QueryTicketSpentRequest{KeyId: 3, TicketKey: bad})
		require.Error(t, err, name)
	}
	_, err = qs.TicketSpent(f.ctx, nil)
	require.Error(t, err)
}
