package service

import (
	"context"
	"crypto/subtle"
	"strings"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type doneBotTeamExecutionContext struct{}
type doneBotTeamExecutionScope struct {
	token     string
	key       *APIKey
	execution donebotteam.Execution
	completed atomic.Bool
}

// TeamExecutionContext is called only after the gateway resolves and authorizes an execution grant from its database.
func (s *DoneBotService) TeamExecutionContext(ctx context.Context, token string, execution *donebotteam.Execution) (context.Context, error) {
	if s.cfg == nil || s.cfg.RunMode == config.RunModeSimple || s.cfg.DoneBot.CatalogCurrency != "CNY" {
		return nil, ErrDoneBotTeamBillingUnavailable
	}
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

type doneBotTeamUsageContext struct{}

// CopyDoneBotTeamUsageContext carries only the immutable receipt ID into billing workers, never execution or account credentials.
func CopyDoneBotTeamUsageContext(parent, base context.Context) context.Context {
	if id := DoneBotTeamUsageExecutionID(parent); id != "" {
		return context.WithValue(base, doneBotTeamUsageContext{}, id)
	}
	return base
}

func DoneBotTeamUsageExecutionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(doneBotTeamUsageContext{}).(string); ok {
		return id
	}
	if scope, ok := ctx.Value(doneBotTeamExecutionContext{}).(*doneBotTeamExecutionScope); ok {
		return scope.execution.ID
	}
	return ""
}

func validateDoneBotTeamUsage(ctx context.Context, key *APIKey, subscription *UserSubscription, cfg *config.Config, repo UsageBillingRepository) error {
	id := DoneBotTeamUsageExecutionID(ctx)
	teamKey := key != nil && isDoneBotTeamKey(key.Key)
	if id == "" && !teamKey {
		return nil
	}
	if id == "" || !teamKey || subscription != nil || repo == nil || cfg == nil || cfg.RunMode == config.RunModeSimple || cfg.DoneBot.CatalogCurrency != "CNY" {
		return ErrDoneBotTeamBillingUnavailable
	}
	return nil
}

func doneBotTeamModelCompleted(ctx context.Context) {
	if scope, ok := ctx.Value(doneBotTeamExecutionContext{}).(*doneBotTeamExecutionScope); ok {
		scope.completed.Store(true)
	}
}

func DoneBotTeamModelCompleted(ctx context.Context) bool {
	scope, ok := ctx.Value(doneBotTeamExecutionContext{}).(*doneBotTeamExecutionScope)
	return ok && scope.completed.Load()
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
