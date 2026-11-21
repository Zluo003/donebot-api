package service

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type executionKeyFake struct{ *doneBotKeyFake }

func (f executionKeyFake) GetByKeyForAuth(_ context.Context, secret string) (*APIKey, error) {
	for _, key := range f.keys {
		if key.Key == secret {
			copy := *key
			return &copy, nil
		}
	}
	return nil, ErrAPIKeyNotFound
}

func TestTeamExecutionKeyRequiresResolvedMatchingScopeAndFreshIdentity(t *testing.T) {
	s, _, keys, team := teamBillingFixture()
	s.keys.apiKeyRepo = executionKeyFake{keys}
	ctx := context.Background()
	token := "dbte_" + strings.Repeat("a", 43)
	e := &donebotteam.Execution{ID: uuid.NewString(), TeamID: team, PayerID: 42, ActorID: 43, Purpose: "llm", CoordinatorID: sql.NullString{String: uuid.NewString(), Valid: true}}
	scoped, err := s.TeamExecutionContext(ctx, token, e)
	require.NoError(t, err)
	require.Equal(t, e.ID, scoped.Value(ctxkey.ClientRequestID))
	key, err := s.keys.GetByKey(scoped, token)
	require.NoError(t, err)
	require.Equal(t, int64(42), key.UserID)
	require.True(t, IsDoneBotModelKey(scoped, key))
	require.False(t, IsDoneBotModelKey(ctx, key))
	for _, attempt := range []struct {
		ctx context.Context
		key string
	}{{ctx, token}, {scoped, key.Key}, {ctx, key.Key}, {scoped, token + "x"}} {
		_, err := s.keys.GetByKey(attempt.ctx, attempt.key)
		require.ErrorIs(t, err, ErrAPIKeyNotFound)
	}
	copy, _, ok := DoneBotTeamExecution(scoped)
	require.True(t, ok)
	copy.PayerID = 999
	current, _, _ := DoneBotTeamExecution(scoped)
	require.Equal(t, int64(42), current.PayerID)
	delete(keys.keys, key.ID)
	_, err = s.keys.GetByKey(scoped, token)
	require.Error(t, err, "execution auth must not use a cached team key after revocation")
}
