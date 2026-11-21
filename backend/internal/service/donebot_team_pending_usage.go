package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// DoneBotTeamPendingUsage freezes a bill before execution leaves the request path.
// Recovery applies this command; it never recalculates prices or contacts a model.
type DoneBotTeamPendingUsage struct {
	ExecutionID string
	Command     UsageBillingCommand
	Usage       DoneBotTeamUsageSnapshot
	Platform    string
}

type DoneBotTeamUsageRepository interface {
	StageTeamUsage(context.Context, *DoneBotTeamPendingUsage) error
	PendingTeamUsage(context.Context, int) ([]string, error)
	ApplyTeamUsage(context.Context, string) (*DoneBotTeamPendingUsage, *UsageBillingApplyResult, error)
	FinishTeamUsage(context.Context, string) error
	RetryTeamUsage(context.Context, string) error
}

func applyDoneBotTeamUsage(ctx context.Context, cmd *UsageBillingCommand, log *UsageLog, p *postUsageBillingParams, deps *billingDeps, repo UsageBillingRepository) (bool, error) {
	durable, ok := repo.(DoneBotTeamUsageRepository)
	if !ok || log == nil {
		return false, ErrDoneBotTeamBillingUnavailable
	}
	pending := &DoneBotTeamPendingUsage{ExecutionID: DoneBotTeamUsageExecutionID(ctx), Command: *cmd, Usage: snapshotDoneBotTeamUsage(log), Platform: p.Platform}
	pending.Usage.ActualCost = cmd.BalanceCost
	if err := durable.StageTeamUsage(ctx, pending); err != nil {
		return false, err
	}
	persisted, result, err := durable.ApplyTeamUsage(ctx, pending.ExecutionID)
	if err != nil {
		retryDoneBotTeamUsage(durable, pending.ExecutionID)
		return false, err
	}
	var invalidator APIKeyAuthCacheInvalidator
	if value, ok := p.APIKeyService.(APIKeyAuthCacheInvalidator); ok {
		invalidator = value
	}
	if err := finishDoneBotTeamUsage(ctx, durable, persisted, deps.billingCacheService, invalidator, deps.deferredService); err != nil {
		retryDoneBotTeamUsage(durable, pending.ExecutionID)
		logger.LegacyPrintf("donebot.team.billing", "Team billing cache refresh pending execution=%s: %v", pending.ExecutionID, err)
	}
	if result != nil && result.Applied {
		go notifyBalanceLow(p, deps, result)
		go notifyAccountQuota(p, deps, result)
	}
	return result != nil && result.Applied, nil
}

func finishDoneBotTeamUsage(ctx context.Context, repo DoneBotTeamUsageRepository, pending *DoneBotTeamPendingUsage, cache *BillingCacheService, auth APIKeyAuthCacheInvalidator, deferred *DeferredService) error {
	cmd := &pending.Command
	if cache != nil {
		if err := cache.InvalidateUserBalance(ctx, cmd.UserID); err != nil {
			return err
		}
		if err := cache.InvalidateAPIKeyRateLimit(ctx, cmd.APIKeyID); err != nil {
			return err
		}
	}
	if auth != nil {
		auth.InvalidateAuthCacheByUserID(ctx, cmd.UserID)
	}
	if deferred != nil {
		deferred.ScheduleLastUsedUpdate(cmd.AccountID)
	}
	return repo.FinishTeamUsage(ctx, pending.ExecutionID)
}

func retryDoneBotTeamUsage(repo DoneBotTeamUsageRepository, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := repo.RetryTeamUsage(ctx, id); err != nil {
		logger.LegacyPrintf("donebot.team.billing", "Team billing retry scheduling failed execution=%s: %v", id, err)
	}
}

type DoneBotTeamBillingRecovery struct {
	repo      DoneBotTeamUsageRepository
	cache     *BillingCacheService
	auth      APIKeyAuthCacheInvalidator
	deferred  *DeferredService
	lifecycle sync.Mutex
	stopped   bool
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	stop      sync.Once
}

func NewDoneBotTeamBillingRecovery(repo UsageBillingRepository, cache *BillingCacheService, auth *APIKeyService, deferred *DeferredService) *DoneBotTeamBillingRecovery {
	durable, _ := repo.(DoneBotTeamUsageRepository)
	svc := &DoneBotTeamBillingRecovery{repo: durable, cache: cache, deferred: deferred}
	if auth != nil {
		svc.auth = auth
	}
	return svc
}

func (s *DoneBotTeamBillingRecovery) Start() {
	if s == nil || s.repo == nil {
		return
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.stopped || s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if err := s.Recover(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.LegacyPrintf("donebot.team.billing", "Team billing recovery failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *DoneBotTeamBillingRecovery) Recover(ctx context.Context) error {
	if s == nil || s.repo == nil {
		return nil
	}
	ids, err := s.repo.PendingTeamUsage(ctx, 32)
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
		pending, _, err := s.repo.ApplyTeamUsage(attempt, id)
		if err == nil {
			err = finishDoneBotTeamUsage(attempt, s.repo, pending, s.cache, s.auth, s.deferred)
		}
		cancel()
		if err != nil {
			retryDoneBotTeamUsage(s.repo, id)
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *DoneBotTeamBillingRecovery) Stop() {
	if s == nil {
		return
	}
	s.stop.Do(func() {
		s.lifecycle.Lock()
		s.stopped = true
		cancel := s.cancel
		s.lifecycle.Unlock()
		if cancel != nil {
			cancel()
		}
		s.wg.Wait()
	})
}
