package service

import (
	"context"
	"sort"
	"time"
)

type doneBotPriorityKey struct{}

// WithDoneBotPriority applies the product's numeric account order after authentication.
func WithDoneBotPriority(ctx context.Context) context.Context {
	return context.WithValue(ctx, doneBotPriorityKey{}, true)
}

func DoneBotPriorityEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(doneBotPriorityKey{}).(bool)
	return enabled || DoneBotMediaReceiptID(ctx) != ""
}

func (s *OpenAIGatewayService) selectDoneBotPriority(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	decision := OpenAIAccountScheduleDecision{Layer: "donebot_priority"}
	if s.checkChannelPricingRestriction(ctx, req.GroupID, req.RequestedModel) {
		return nil, decision, ErrNoAvailableAccounts
	}
	accounts, err := s.listSchedulableAccounts(ctx, req.GroupID, req.Platform)
	if err != nil {
		return nil, decision, err
	}
	scheduler := &defaultOpenAIAccountScheduler{service: s}
	candidates := make([]*Account, 0, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		if _, excluded := req.ExcludedIDs[a.ID]; excluded {
			continue
		}
		if !a.IsSchedulable() || !s.openAIAccountMatchesSchedulingGroup(a, req.GroupID) || a.Platform != req.Platform || !a.IsOpenAICompatible() {
			continue
		}
		if !scheduler.isAccountTransportCompatible(a, req.RequiredTransport) || !scheduler.isAccountRequestCompatible(ctx, a, req) {
			continue
		}
		candidates = append(candidates, a)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].ID < candidates[j].ID
	})
	decision.CandidateCount = len(candidates)
	for _, candidate := range candidates {
		a := s.resolveFreshSchedulableOpenAIAccount(ctx, candidate, req.Platform, req.RequestedModel, req.RequireCompact, req.RequiredCapability)
		if a == nil {
			continue
		}
		a = s.recheckSelectedOpenAIAccountFromDB(ctx, a, req.GroupID, req.Platform, req.RequestedModel, req.RequireCompact, req.RequiredCapability)
		if a == nil || !scheduler.isAccountTransportCompatible(a, req.RequiredTransport) || !scheduler.isAccountRequestCompatible(ctx, a, req) {
			continue
		}
		slot, err := s.tryAcquireAccountSlot(ctx, a.ID, a.Concurrency)
		if err != nil {
			return nil, decision, err
		}
		decision.SelectedAccountID = a.ID
		decision.SelectedAccountType = a.Type
		if slot != nil && slot.Acquired {
			return attachSelectionProfitGate(ctx, &AccountSelectionResult{Account: a, Acquired: true, ReleaseFunc: slot.ReleaseFunc}), decision, nil
		}
		return attachSelectionProfitGate(ctx, &AccountSelectionResult{Account: a, WaitPlan: &AccountWaitPlan{AccountID: a.ID, MaxConcurrency: a.Concurrency, Timeout: 30 * time.Second, MaxWaiting: 100}}), decision, nil
	}
	return nil, decision, ErrNoAvailableAccounts
}

func (s *GatewayService) selectDoneBotGatewayPriority(ctx context.Context, groupID *int64, model, platform string, excluded map[int64]struct{}) (*Account, error) {
	accounts, mixed, err := s.listSchedulableAccounts(ctx, groupID, platform, false)
	if err != nil {
		return nil, err
	}
	ctx = s.withWindowCostPrefetch(ctx, accounts)
	ctx = s.withRPMPrefetch(ctx, accounts)
	routes := s.routingAccountIDsForRequest(ctx, groupID, model, platform)
	var group *Group
	if groupID != nil && s.groupRepo != nil {
		group, err = s.groupRepo.GetByIDLite(ctx, *groupID)
		if err != nil {
			return nil, err
		}
	}
	var selected *Account
	for i := range accounts {
		a := &accounts[i]
		if _, skip := excluded[a.ID]; skip {
			continue
		}
		if len(routes) > 0 && !containsInt64(routes, a.ID) {
			continue
		}
		if !s.isAccountInGroup(a, groupID) || !s.isAccountSchedulableForSelection(a) || !s.isGatewayAccountProfitEligible(ctx, a) {
			continue
		}
		if a.Platform != platform && (!mixed || a.Platform != PlatformAntigravity || !a.IsMixedSchedulingEnabled()) {
			continue
		}
		if group != nil && group.RequirePrivacySet && !a.IsPrivacySet() {
			continue
		}
		if model != "" && !s.isModelSupportedByAccountWithContext(ctx, a, model) {
			continue
		}
		if !s.isAccountSchedulableForModelSelection(ctx, a, model) || !s.isAccountSchedulableForQuota(a) || !s.isAccountSchedulableForWindowCost(ctx, a, false) || !s.isAccountSchedulableForRPM(ctx, a, false) || s.isStickyAccountUpstreamRestricted(ctx, groupID, a, model) {
			continue
		}
		if selected == nil || a.Priority < selected.Priority || a.Priority == selected.Priority && a.ID < selected.ID {
			selected = a
		}
	}
	if selected == nil {
		return nil, ErrNoAvailableAccounts
	}
	return s.hydrateSelectedAccount(ctx, selected)
}

// DoneBotAccountSwitchLimit lets an authenticated product request exhaust its eligible accounts.
func DoneBotAccountSwitchLimit(ctx context.Context, configured int) int {
	if DoneBotPriorityEnabled(ctx) {
		return int(^uint(0) >> 1)
	}
	return configured
}
