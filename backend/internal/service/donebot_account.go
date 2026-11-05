package service

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type DoneBotAccount struct {
	Concurrency            int                 `json:"concurrency"`
	ID                     string              `json:"id"`
	Currency               string              `json:"currency"`
	BalanceCents           json.Number         `json:"balance_cents"`
	FrozenCents            json.Number         `json:"frozen_cents"`
	GroupStatus            string              `json:"group_status"`
	CatalogScopeID         string              `json:"catalog_scope_id"`
	CatalogVersion         string              `json:"catalog_version"`
	GatewayContractVersion int                 `json:"gateway_contract_version"`
	Usage                  DoneBotAccountUsage `json:"usage"`
}

type DoneBotAccountUsage struct {
	Requests     int64       `json:"requests"`
	InputTokens  int64       `json:"input_tokens"`
	OutputTokens int64       `json:"output_tokens"`
	ActualCents  json.Number `json:"actual_cents"`
}

type DoneBotReceipt struct {
	ReceiptID          string       `json:"receipt_id"`
	RequestID          string       `json:"request_id"`
	InstallationID     string       `json:"installation_id"`
	Status             string       `json:"status"`
	Model              string       `json:"model"`
	Currency           string       `json:"currency"`
	EstimatedCents     *json.Number `json:"estimated_cents"`
	ActualCents        json.Number  `json:"actual_cents"`
	RequestedReasoning *string      `json:"requested_reasoning"`
	EffectiveReasoning *string      `json:"effective_reasoning"`
}

type doneBotUsageReader interface {
	ListWithFilters(context.Context, pagination.PaginationParams, usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error)
	GetStatsWithFilters(context.Context, usagestats.UsageLogFilters) (*usagestats.UsageStats, error)
}

type DoneBotAccountService struct {
	installs *DoneBotService
	catalog  *DoneBotCatalogService
	usage    doneBotUsageReader
}

func NewDoneBotAccountService(installs *DoneBotService, catalog *DoneBotCatalogService, usage *UsageService) *DoneBotAccountService {
	return &DoneBotAccountService{installs: installs, catalog: catalog, usage: usage}
}

func doneBotCents(yuan float64) (json.Number, error) {
	if math.IsNaN(yuan) || math.IsInf(yuan, 0) {
		return "", infraerrors.ServiceUnavailable("DONEBOT_AMOUNT_INVALID", "DoneBot amount is unavailable")
	}
	return json.Number(decimal.NewFromFloat(yuan).Mul(decimal.NewFromInt(100)).String()), nil
}

func (s *DoneBotAccountService) Account(ctx context.Context, userID int64, installationUUID string) (*DoneBotAccount, error) {
	catalog, err := s.catalog.Models(ctx, userID, installationUUID)
	if err != nil {
		return nil, err
	}
	user, err := s.installs.keys.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive() {
		return nil, ErrDoneBotUserInactive
	}
	balance, err := doneBotCents(user.Balance)
	if err != nil {
		return nil, err
	}
	frozen, err := doneBotCents(user.FrozenBalance)
	if err != nil {
		return nil, err
	}
	install, err := s.installs.InstallationForUser(ctx, userID, installationUUID)
	if err != nil {
		return nil, err
	}
	stats, err := s.usage.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{UserID: userID, GroupID: install.GroupID})
	if err != nil {
		return nil, err
	}
	if stats == nil {
		return nil, infraerrors.ServiceUnavailable("DONEBOT_USAGE_UNAVAILABLE", "DoneBot usage is unavailable")
	}
	actual, err := doneBotCents(stats.TotalActualCost)
	if err != nil {
		return nil, err
	}
	return &DoneBotAccount{Concurrency: user.Concurrency, ID: strconv.FormatInt(user.ID, 10), Currency: "CNY", BalanceCents: balance, FrozenCents: frozen,
		Usage:       DoneBotAccountUsage{Requests: stats.TotalRequests, InputTokens: stats.TotalInputTokens, OutputTokens: stats.TotalOutputTokens, ActualCents: actual},
		GroupStatus: StatusActive, CatalogScopeID: catalog.CatalogScopeID, CatalogVersion: catalog.CatalogVersion, GatewayContractVersion: 3}, nil
}

func (s *DoneBotAccountService) Receipt(ctx context.Context, userID int64, installationUUID, receiptID string) (*DoneBotReceipt, error) {
	if _, err := s.catalog.Models(ctx, userID, installationUUID); err != nil {
		return nil, err
	}
	install, err := s.installs.InstallationForUser(ctx, userID, installationUUID)
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(receiptID); err != nil {
		return nil, ErrUsageLogNotFound
	}
	if s.installs.cfg.DoneBot.CatalogCurrency != "CNY" {
		return nil, ErrDoneBotCatalogCurrency
	}
	logs, _, err := s.usage.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 1}, usagestats.UsageLogFilters{
		UserID: userID, APIKeyID: install.APIKeyID, GroupID: install.GroupID, RequestID: "client:" + receiptID,
	})
	if err != nil {
		return nil, err
	}
	if len(logs) == 0 {
		return nil, ErrUsageLogNotFound
	}
	log := &logs[0]
	if log.UserID != userID || log.APIKeyID != install.APIKeyID || log.GroupID == nil || *log.GroupID != install.GroupID || log.RequestID != "client:"+receiptID {
		return nil, ErrUsageLogNotFound
	}
	return doneBotReceiptFromUsage(log, installationUUID, receiptID)
}

func doneBotReceiptFromUsage(log *UsageLog, installationUUID, receiptID string) (*DoneBotReceipt, error) {
	amount, err := doneBotCents(log.ActualCost)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(log.RequestedModel)
	if model == "" {
		model = log.Model
	}
	return &DoneBotReceipt{ReceiptID: receiptID, RequestID: log.RequestID, InstallationID: installationUUID,
		Status: "completed", Model: model, Currency: "CNY", ActualCents: amount,
		RequestedReasoning: log.RequestedReasoningEffort, EffectiveReasoning: log.ReasoningEffort}, nil
}
