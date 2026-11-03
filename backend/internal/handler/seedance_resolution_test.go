//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSeedanceResolutionAccountAdmission(t *testing.T) {
	for _, tc := range []struct {
		name         string
		blockedCount int
		allBlocked   bool
	}{
		{"skip_ineligible_account", 1, false},
		{"skip_four_ineligible_accounts", 4, false},
		{"skip_eight_ineligible_accounts", 8, false},
		{"all_blocked", 1, true},
		{"all_blocked_many", 8, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eligibleAccountID := int64(tc.blockedCount + 1)
			accounts := make([]service.Account, 0, tc.blockedCount+1)
			for id := int64(1); id <= eligibleAccountID; id++ {
				allowed := []string{"720p"}
				if id == eligibleAccountID && !tc.allBlocked {
					allowed = []string{"1080p"}
				}
				accounts = append(accounts, service.Account{
					ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{24},
					Concurrency: 3, Priority: int(id - 1),
					Credentials: map[string]any{"base_url": service.SeedanceNewTokenBaseURL,
						"api_key": "test-key", "openai_capabilities": []string{"seedance"},
						"seedance_allowed_resolutions": allowed},
				})
			}
			repo := openAIImagesFailoverAccountRepo{accounts: accounts}
			slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
			concurrency := service.NewConcurrencyService(slots)
			bindings := &grokMediaSlotBindings{}
			upstream := &grokMediaSlotUpstream{call: func(req *http.Request, accountID int64) (*http.Response, error) {
				require.Equal(t, eligibleAccountID, accountID, "ineligible account must never receive the upstream create")
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"task-resolution"}`))}, nil
			}}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			modelBilling := service.NewBillingService(cfg, nil)
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, bindings, cfg, nil, concurrency, modelBilling, nil, nil, upstream, nil, nil, nil, service.NewModelPricingResolver(nil, modelBilling), nil, nil, nil, nil)
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			c, w := grokMediaSlotContext(context.Background(), true)
			key, _ := middleware.GetAPIKeyFromContext(c)
			key.Group.Platform = service.PlatformOpenAI
			p := 0.1
			key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"doubao-seedance-2-0-260128"}, BillingMode: service.BillingModeVideo, Intervals: []service.PricingInterval{{TierLabel: "1080p|standard", PerRequestPrice: &p}}}}
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v3/contents/generations/tasks", strings.NewReader(`{"model":"doubao-seedance-2-0-260128","resolution":"1080p","content":[{"type":"text","text":"waves"}]}`))
			c.Request.Header.Set("Content-Type", "application/json")
			h.SeedanceTasks(c)
			if tc.allBlocked {
				require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
				require.Zero(t, upstream.calls)
			} else {
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				require.Equal(t, 1, upstream.calls)
			}
			slots.assertReleased(t)
		})
	}
}
