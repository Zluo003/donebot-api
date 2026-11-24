package service

import (
	"context"
	"strings"
	"sync"
)

const (
	DoneBotMarketInstallHeader = "X-DoneBot-Market-Install"
	DoneBotBotIDHeader         = "X-DoneBot-Bot-Id"
)

type doneBotMarketInstallTokenKey struct{}
type doneBotBotIDKey struct{}

// DoneBotMarketBillingHook decouples core usage/media billing settlement from the bot market ledger and per-bot spend tracking.
type DoneBotMarketBillingHook interface {
	OnLLMSettled(ctx context.Context, installerUserID int64, usageKey string, actualCost float64, tokens int64, installToken string, botID string)
	OnMediaAdmitted(ctx context.Context, receiptID string, installerUserID int64, category string, installToken string, botID string)
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

func WithDoneBotBotID(ctx context.Context, botID string) context.Context {
	trimmed := strings.TrimSpace(botID)
	if trimmed == "" || len(trimmed) > 128 {
		return ctx
	}
	return context.WithValue(ctx, doneBotBotIDKey{}, trimmed)
}

func DoneBotBotID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(doneBotBotIDKey{}).(string)
	return strings.TrimSpace(v)
}

func CopyDoneBotMarketContext(parent, base context.Context) context.Context {
	out := base
	if token := DoneBotMarketInstallToken(parent); token != "" {
		out = WithDoneBotMarketInstallToken(out, token)
	}
	if botID := DoneBotBotID(parent); botID != "" {
		out = WithDoneBotBotID(out, botID)
	}
	return out
}

func NotifyDoneBotMarketLLMSettled(ctx context.Context, installerUserID int64, usageKey string, actualCost float64, tokens int64) {
	token := DoneBotMarketInstallToken(ctx)
	botID := DoneBotBotID(ctx)
	if (token == "" && botID == "") || installerUserID <= 0 || actualCost <= 0 || strings.TrimSpace(usageKey) == "" {
		return
	}
	hook := currentDoneBotMarketBillingHook()
	if hook == nil {
		return
	}
	if tokens < 0 {
		tokens = 0
	}
	hook.OnLLMSettled(ctx, installerUserID, usageKey, actualCost, tokens, token, botID)
}

func NotifyDoneBotMarketMediaAdmitted(ctx context.Context, receiptID string, installerUserID int64, category string) {
	token := DoneBotMarketInstallToken(ctx)
	botID := DoneBotBotID(ctx)
	if (token == "" && botID == "") || strings.TrimSpace(receiptID) == "" || installerUserID <= 0 {
		return
	}
	hook := currentDoneBotMarketBillingHook()
	if hook == nil {
		return
	}
	hook.OnMediaAdmitted(ctx, receiptID, installerUserID, category, token, botID)
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
