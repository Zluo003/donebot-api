//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSeedanceReferenceCreateUsesTrustedInputForAdmissionAndPersistence(t *testing.T) {
	for _, scenario := range []string{"accepted", "insufficient_balance", "duration_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI)
			h.cfg.RunMode = config.RunModeStandard
			h.cfg.DoneBot.Enabled = true
			h.cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
			balance := 10.0
			if scenario == "insufficient_balance" {
				balance = 1.5 // Covers six output seconds ($1.2), but not inputs + output ($2.45).
			}
			cache := newHandlerInflightCache(balance)
			billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, h.cfg, nil)
			t.Cleanup(billing.Stop)
			h.billingCacheService = billing
			resolverCalls := 0
			h.seedanceReferenceDurationResolver = func(_ context.Context, account *service.Account, info *service.GrokMediaRequestInfo) (float64, error) {
				resolverCalls++
				require.Positive(t, account.ID, "trusted metadata must come from the selected upstream account")
				require.Equal(t, []string{"asset://source-one", "https://cdn.example/source-two.mp4"}, info.InputVideoURLs)
				require.Zero(t, info.ReferenceVideoDurationSeconds, "native caller duration hints are untrusted")
				if scenario == "duration_unavailable" {
					return 0, errors.New("metadata unavailable for https://cdn.example/source-two.mp4?sig=private-secret")
				}
				return 2.5 + 3.75, nil
			}
			body := `{"model":"doubao-seedance-2-5-260628","resolution":"720p","duration":6,"reference_video_duration":0.01,"content":[{"type":"text","text":"follow this movement"},{"type":"video_url","video_url":{"url":"asset://source-one","duration":0.01},"role":"reference_video"},{"type":"video_url","video_url":{"url":"https://cdn.example/source-two.mp4","duration":0.01},"role":"reference_video"}]}`
			var forwarded []byte
			upstream.call = func(request *http.Request, _ int64) (*http.Response, error) {
				var err error
				forwarded, err = io.ReadAll(request.Body)
				require.NoError(t, err)
				cache.mu.Lock()
				reserved := 0.0
				for _, amount := range cache.res {
					reserved += amount
				}
				cache.mu.Unlock()
				require.InDelta(t, (2.5+3.75+6)*.2, reserved, 1e-12, "reserve all input and output seconds before sending create")
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"reference-task","status":"queued"}`))}, nil
			}
			c, response := grokMediaSlotContext(context.Background(), true)
			key, _ := middleware.GetAPIKeyFromContext(c)
			key.Group.Platform = service.PlatformOpenAI
			key.Group.RateMultiplier = 1
			price := .2
			key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"doubao-seedance-2-5-260628"}, BillingMode: service.BillingModeVideo, Intervals: []service.PricingInterval{{TierLabel: "720p|reference", PerRequestPrice: &price}}}}
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v3/contents/generations/tasks", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			h.SeedanceTasks(c)
			if reservation := service.InflightReservationFromContext(c.Request.Context()); reservation != nil {
				t.Cleanup(reservation.Release)
			}
			require.Equal(t, 1, resolverCalls)
			slots.assertReleased(t)
			if scenario != "accepted" {
				status := http.StatusForbidden // Preserve the upstream gateway's billing error contract.
				if scenario == "duration_unavailable" {
					status = http.StatusBadRequest
				}
				require.Equal(t, status, response.Code, response.Body.String())
				if scenario == "insufficient_balance" {
					require.Contains(t, response.Body.String(), `"type":"billing_error"`)
				}
				require.Zero(t, upstream.calls, "failed metering/admission must never create a paid upstream task")
				require.Empty(t, bindings.pending)
				require.Empty(t, bindings.jobs)
				require.Zero(t, cache.count())
				require.NotContains(t, response.Body.String(), "private-secret")
				return
			}
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, 1, upstream.calls)
			require.Equal(t, gjson.Get(body, "content").Raw, gjson.GetBytes(forwarded, "content").Raw, "trusted metering must not rewrite the native material protocol")
			require.Len(t, bindings.pending, 1)
			for _, encoded := range bindings.pending {
				var pending service.GrokVideoPendingBilling
				require.NoError(t, json.Unmarshal(encoded, &pending))
				require.InDelta(t, 6.25, pending.VideoInputDurationSeconds, 1e-12)
				require.True(t, pending.VideoReference)
			}
			require.Len(t, bindings.jobs, 1)
			for _, job := range bindings.jobs {
				require.NotNil(t, job.VideoPending)
				require.InDelta(t, 6.25, job.VideoPending.VideoInputDurationSeconds, 1e-12)
				require.NotNil(t, job.Reservation)
				require.InDelta(t, (6.25+6)*.2, job.Reservation.Amount, 1e-12)
			}
			require.Equal(t, 1, cache.count(), "accepted tasks retain their reservation for durable settlement")
		})
	}
}
