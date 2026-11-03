//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type mediaSettlementMoney struct {
	service.UsageBillingRepository
	recorded            bool
	lookupErr, applyErr error
	calls               int
	last                *service.UsageBillingCommand
}

func (m *mediaSettlementMoney) HasUsageCharge(context.Context, string, int64) (bool, error) {
	return m.recorded, m.lookupErr
}

func (m *mediaSettlementMoney) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	m.calls++
	m.last = cmd
	if m.applyErr != nil {
		return nil, m.applyErr
	}
	applied := !m.recorded
	m.recorded = true
	return &service.UsageBillingApplyResult{Applied: applied}, nil
}

type mediaSettlementFixture struct {
	h          *OpenAIGatewayHandler
	bindings   *grokMediaSlotBindings
	money      *mediaSettlementMoney
	logs       *observer.ObservedLogs
	taskKey    string
	upstream   *grokMediaSlotUpstream
	nativeBody string
	poll       func() *httptest.ResponseRecorder
}

func newMediaSettlementFixture(t *testing.T, seedance bool) *mediaSettlementFixture {
	t.Helper()
	money := &mediaSettlementMoney{}
	platforms := []string{service.PlatformOpenAI}
	if !seedance {
		platforms = append(platforms, "apimart")
	}
	h, slots, bindings, upstream := newGrokMediaSlotHandlerWithBilling(t, money, false, false, platforms...)
	core, logs := observer.New(zap.DebugLevel)
	ctx := logger.IntoContext(context.Background(), zap.New(core))
	taskKey := service.APIMartTaskKey(service.APIMartImageTask, "task-mart")
	response := `{"code":200,"data":{"status":"success","result":{"images":[{"url":["https://media.example/result.png"]}]}}}`
	if seedance {
		taskKey = service.SeedanceTaskKey("task-mart")
		response = `{"id":"task-mart","status":"succeeded","duration":6,"resolution":"720p","content":{"video_url":"https://media.example/result.mp4"},"usage":{"total_tokens":12345}}`
	}
	upstream.call = func(*http.Request, int64) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	}
	gid := int64(24)
	require.NoError(t, h.gatewayService.BindGrokMediaVideoRequestAccount(ctx, &gid, taskKey, 10, 20, 1))
	if seedance {
		require.NoError(t, h.gatewayService.StoreGrokVideoPendingBilling(ctx, taskKey, 10, 20, service.GrokVideoPendingBilling{Model: "movie", BillingModel: "movie", OriginalModel: "movie", VideoResolution: "720p", CreatedAt: service.GrokVideoPendingCreatedAtNow()}))
	} else {
		require.NoError(t, h.gatewayService.StoreAPIMartPending(ctx, taskKey, 10, 20, service.APIMartPending{Endpoint: service.APIMartImages, Model: "seedream-5-0-flash", OriginalModel: "seedream-5-0-flash", Tier: "2K", CreatedAt: service.GrokVideoPendingCreatedAtNow()}))
	}
	f := &mediaSettlementFixture{h: h, bindings: bindings, money: money, logs: logs, taskKey: taskKey, upstream: upstream, nativeBody: response}
	f.poll = func() *httptest.ResponseRecorder {
		var c *gin.Context
		var w *httptest.ResponseRecorder
		if seedance {
			c, w = grokMediaSlotContext(ctx, false)
			key, _ := middleware.GetAPIKeyFromContext(c)
			key.Group.Platform = service.PlatformOpenAI
			key.Group.RateMultiplier = 1
			price := 0.1
			key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"movie"}, BillingMode: service.BillingModeVideo, Intervals: []service.PricingInterval{{TierLabel: "720p|standard", PerRequestPrice: &price}}}}
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v3/contents/generations/tasks/task-mart", nil).WithContext(ctx)
			c.Params = gin.Params{{Key: "task_id", Value: "task-mart"}}
			h.SeedanceTasks(c)
		} else {
			c, w = apimartHandlerContext("", true)
			c.Request = c.Request.WithContext(ctx)
			h.APIMartImageTask(c)
		}
		slots.assertReleased(t)
		return w
	}
	return f
}

func TestMediaCompletionBillingStorageFailuresDoNotDeliverProducts(t *testing.T) {
	for _, seedance := range []bool{true, false} {
		provider := "apimart"
		if seedance {
			provider = "seedance"
		}
		for _, failure := range []string{"load", "claim", "already claimed", "ledger"} {
			t.Run(provider+"/"+failure, func(t *testing.T) {
				f := newMediaSettlementFixture(t, seedance)
				switch failure {
				case "load":
					f.bindings.pendingErr = errors.New("redis pending unavailable")
				case "claim":
					f.bindings.claimErr = errors.New("redis claim unavailable")
				case "already claimed":
					claimed, err := f.h.gatewayService.ClaimGrokVideoBilling(context.Background(), f.taskKey, 10, 20)
					require.NoError(t, err)
					require.True(t, claimed)
				case "ledger":
					f.money.lookupErr = errors.New("ledger unavailable")
				}
				w := f.poll()
				require.Equal(t, 503, w.Code, w.Body.String())
				require.NotContains(t, w.Body.String(), "https://media.example")
				require.Zero(t, f.money.calls)
				if failure == "already claimed" {
					require.Empty(t, f.logs.FilterLevelExact(zap.ErrorLevel).All(), "another poll holding the claim is a normal race")
				} else {
					require.NotEmpty(t, f.logs.FilterLevelExact(zap.ErrorLevel).All(), "storage failures must be observable")
				}
			})
		}
	}
}

func TestSeedanceMissingPendingFailsClosedUnlessDurablySettled(t *testing.T) {
	f := newMediaSettlementFixture(t, true)
	f.bindings.pending = nil
	w := f.poll()
	require.Equal(t, 503, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "https://media.example")
	require.NotEmpty(t, f.logs.FilterMessage("seedance.video_billing_pending_missing").All())
	// A background worker may already have completed the durable money event.
	// Expired query metadata or a stale claim must not block an already paid task.
	f.money.recorded = true
	f.bindings.pendingErr = errors.New("query cache unavailable")
	f.bindings.claimErr = errors.New("claim cache unavailable")
	w = f.poll()
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "https://media.example/result.mp4")
	require.Zero(t, f.money.calls)
}

func TestSeedanceSucceededProductRequiresMeteringOrDurableCharge(t *testing.T) {
	f := newMediaSettlementFixture(t, true)
	f.upstream.call = func(*http.Request, int64) (*http.Response, error) {
		body := `{"id":"task-mart","status":"succeeded","content":{"video_url":"https://media.example/result.mp4"}}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	w := f.poll()
	require.Equal(t, 503, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "https://media.example")
	require.Zero(t, f.money.calls)
	require.Empty(t, f.bindings.billed, "incomplete metering must not burn a claim")
	f.money.recorded = true
	w = f.poll()
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "https://media.example")
}

func TestSeedanceDurablyPaidStatusPreservesNativeBody(t *testing.T) {
	f := newMediaSettlementFixture(t, true)
	f.money.recorded = true
	w := f.poll()
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, f.nativeBody, w.Body.String())
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))
	require.Zero(t, f.money.calls)
}

func TestMediaCompletionFailedBillingReleasesClaimAndRetries(t *testing.T) {
	for _, seedance := range []bool{true, false} {
		for _, releaseFails := range []bool{false, true} {
			name := "apimart"
			if seedance {
				name = "seedance"
			}
			if releaseFails {
				name += "/failed release"
			}
			t.Run(name, func(t *testing.T) {
				f := newMediaSettlementFixture(t, seedance)
				f.money.applyErr = errors.New("billing transaction unavailable")
				if releaseFails {
					f.bindings.releaseErr = errors.New("redis release unavailable")
				}
				w := f.poll()
				require.Equal(t, 503, w.Code, w.Body.String())
				require.NotContains(t, w.Body.String(), "https://media.example")
				require.Equal(t, 1, f.bindings.releases)
				require.Equal(t, 1, f.money.calls)
				requestID := f.taskKey
				if seedance {
					requestID = service.StableGrokVideoBillingRequestID(requestID)
				}
				require.Equal(t, requestID, f.money.last.RequestID, "polls and the worker must share the existing durable billing ID")
				if releaseFails {
					require.NotEmpty(t, f.logs.FilterMessage(name[:strings.Index(name, "/")]+".video_billing_claim_release_failed").All())
					// Simulate the existing durable worker committing independently of
					// the stuck short-lived claim; the next poll must recover delivery.
					f.money.recorded = true
				}
				f.money.applyErr = nil
				w = f.poll()
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), "https://media.example")
				calls := f.money.calls
				w = f.poll()
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Equal(t, calls, f.money.calls, "repeated polls share the durable money event")
			})
		}
	}
}
