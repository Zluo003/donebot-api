package service

import (
	"context"
	"crypto/subtle"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type doneBotTeamExecutionContext struct{}
type doneBotTeamExecutionScope struct {
	token     string
	key       *APIKey
	execution donebotteam.Execution
}

// TeamExecutionContext is called only after the gateway resolves and authorizes an execution grant from its database.
func (s *DoneBotService) TeamExecutionContext(ctx context.Context, token string, execution *donebotteam.Execution) (context.Context, error) {
	if execution == nil || execution.Purpose != "llm" || !execution.CoordinatorID.Valid {
		return nil, ErrAPIKeyNotFound
	}
	key, err := s.TeamBillingKey(ctx, execution.TeamID)
	if err != nil {
		return nil, err
	}
	if key.UserID != execution.PayerID {
		return nil, ErrAPIKeyNotFound
	}
	scope := &doneBotTeamExecutionScope{token: token, key: key, execution: *execution}
	ctx = context.WithValue(ctx, doneBotTeamExecutionContext{}, scope)
	return context.WithValue(ctx, ctxkey.ClientRequestID, execution.ID), nil
}

func DoneBotTeamExecution(ctx context.Context) (*donebotteam.Execution, string, bool) {
	scope, ok := ctx.Value(doneBotTeamExecutionContext{}).(*doneBotTeamExecutionScope)
	if !ok {
		return nil, "", false
	}
	e := scope.execution
	return &e, scope.token, true
}

func (s *APIKeyService) teamExecutionKey(ctx context.Context, token string) (*APIKey, error) {
	scope, ok := ctx.Value(doneBotTeamExecutionContext{}).(*doneBotTeamExecutionScope)
	if !ok || subtle.ConstantTimeCompare([]byte(scope.token), []byte(token)) != 1 {
		return nil, ErrAPIKeyNotFound
	}
	key, err := s.lookupAPIKeyForAuth(ctx, scope.key.Key)
	if err != nil {
		return nil, err
	}
	if key == nil || key.ID != scope.key.ID || key.UserID != scope.execution.PayerID || key.GroupID == nil || scope.key.GroupID == nil || *key.GroupID != *scope.key.GroupID {
		return nil, ErrAPIKeyNotFound
	}
	key.Key = scope.key.Key
	s.compileAPIKeyIPRules(key)
	return key, nil
}

func IsDoneBotModelKey(ctx context.Context, key *APIKey) bool {
	if key == nil {
		return false
	}
	if strings.HasPrefix(key.Key, "sk-donebot-") {
		return true
	}
	scope, ok := ctx.Value(doneBotTeamExecutionContext{}).(*doneBotTeamExecutionScope)
	return ok && key.ID == scope.key.ID && key.UserID == scope.execution.PayerID && isDoneBotTeamKey(key.Key)
}
