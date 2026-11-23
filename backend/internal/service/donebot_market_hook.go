package service

import (
	"context"
	"strings"
	"sync"
)

const DoneBotMarketInstallHeader = "X-DoneBot-Market-Install"

type doneBotMarketInstallTokenKey struct{}

// DoneBotMarketBillingHook decouples core usage/media billing settlement from the bot market ledger.
type DoneBotMarketBillingHook interface {
	OnLLMSettled(ctx context.Context, installerUserID int64, usageKey string, actualCost float64, installToken string)
	OnMediaAdmitted(ctx context.Context, receiptID string, installerUserID int64, category string, installToken string)
	OnMediaSettled(ctx context.Context, receiptID string, installerUserID int64, actualCost float64, failed bool)
}

var (
	doneBotMarketHookMu sync.RWMutex
	doneBotMarketHook   DoneBotMarketBillingHook
)

func RegisterDoneBotMarketBillingHook(hook DoneBotMarketBillingHook) {
	doneBotMarketHookMu.Lock()
	doneBotMarketHook = hook
	doneBotMarketHookMu.Unlock()
}

func currentDoneBotMarketBillingHook() DoneBotMarketBillingHook {
	doneBotMarketHookMu.RLock()
	h := doneBotMarketHook
	doneBotMarketHookMu.RUnlock()
	return h
}

func WithDoneBotMarketInstallToken(ctx context.Context, token string) context.Context {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" || len(trimmed) > 1024 {
		return ctx
	}
	return context.WithValue(ctx, doneBotMarketInstallTokenKey{}, trimmed)
}

func DoneBotMarketInstallToken(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(doneBotMarketInstallTokenKey{}).(string)
	return strings.TrimSpace(v)
}

func CopyDoneBotMarketContext(parent, base context.Context) context.Context {
	if token := DoneBotMarketInstallToken(parent); token != "" {
		return WithDoneBotMarketInstallToken(base, token)
	}
	return base
}

func NotifyDoneBotMarketLLMSettled(ctx context.Context, installerUserID int64, usageKey string, actualCost float64) {
	token := DoneBotMarketInstallToken(ctx)
	if token == "" || installerUserID <= 0 || actualCost <= 0 || strings.TrimSpace(usageKey) == "" {
		return
	}
	hook := currentDoneBotMarketBillingHook()
	if hook == nil {
		return
	}
	hook.OnLLMSettled(ctx, installerUserID, usageKey, actualCost, token)
}

func NotifyDoneBotMarketMediaAdmitted(ctx context.Context, receiptID string, installerUserID int64, category string) {
	token := DoneBotMarketInstallToken(ctx)
	if token == "" || strings.TrimSpace(receiptID) == "" || installerUserID <= 0 {
		return
	}
	hook := currentDoneBotMarketBillingHook()
	if hook == nil {
		return
	}
	hook.OnMediaAdmitted(ctx, receiptID, installerUserID, category, token)
}

func NotifyDoneBotMarketMediaSettled(ctx context.Context, receiptID string, installerUserID int64, actualCost float64, failed bool) {
	if strings.TrimSpace(receiptID) == "" || installerUserID <= 0 {
		return
	}
	hook := currentDoneBotMarketBillingHook()
	if hook == nil {
		return
	}
	hook.OnMediaSettled(ctx, receiptID, installerUserID, actualCost, failed)
}
