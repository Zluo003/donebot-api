package donebotmarket

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

var (
	sharedMu  sync.Mutex
	sharedMap = map[*sql.DB]*Service{}
)

// Shared returns a process-wide Service per *sql.DB so user & admin handlers share
// the same background AI review worker and kick channel without duplicating goroutines.
func Shared(db *sql.DB, enabled bool) *Service {
	if db == nil {
		return New(nil, false)
	}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if existing, ok := sharedMap[db]; ok {
		if enabled && !existing.enabled {
			existing.enabled = true
			existing.StartBackgroundWorkers()
		}
		return existing
	}
	svc := New(db, enabled)
	if enabled {
		svc.StartBackgroundWorkers()
	}
	sharedMap[db] = svc
	return svc
}

// BillingHookAdapter bridges service.DoneBotMarketBillingHook to Service without import cycles.
type BillingHookAdapter struct {
	svc *Service
}

func NewBillingHookAdapter(svc *Service) *BillingHookAdapter {
	return &BillingHookAdapter{svc: svc}
}

func (a *BillingHookAdapter) OnLLMSettled(ctx context.Context, installerUserID int64, usageKey string, actualCost float64, tokens int64, installToken string, botID string) {
	if a == nil || a.svc == nil {
		return
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if installToken != "" {
		_ = a.svc.AccrueLLMUsage(runCtx, installerUserID, usageKey, actualCost, tokens, installToken)
	}
	if botID != "" {
		_ = a.svc.RecordBotLLMSpend(runCtx, installerUserID, botID, usageKey, actualCost)
	}
}

func (a *BillingHookAdapter) OnMediaAdmitted(ctx context.Context, receiptID string, installerUserID int64, category string, installToken string, botID string) {
	if a == nil || a.svc == nil {
		return
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if installToken != "" {
		a.svc.RecordMediaAttribution(runCtx, receiptID, installerUserID, category, installToken)
	}
	if botID != "" {
		a.svc.RecordBotMediaSpendRef(runCtx, receiptID, installerUserID, botID, category)
	}
}

func (a *BillingHookAdapter) OnMediaSettled(ctx context.Context, receiptID string, installerUserID int64, actualCost float64, failed bool) {
	if a == nil || a.svc == nil {
		return
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = a.svc.AccrueMediaSettlement(runCtx, receiptID, installerUserID, actualCost, failed)
	_ = a.svc.SettleBotMediaSpend(runCtx, receiptID, installerUserID, actualCost, failed)
}
