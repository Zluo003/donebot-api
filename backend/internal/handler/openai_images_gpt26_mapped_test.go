//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type gpt26PrepayCounter struct {
	service.UsageBillingRepository
	service.DoneBotMediaPrepaidRepository
	calls int
}

func (repo *gpt26PrepayCounter) PrepayDoneBotMedia(context.Context, *service.DoneBotMediaPrecharge) error {
	repo.calls++
	return nil
}

func TestDoneBotGPTImage26InvalidMappedGIFRejectsBeforeDebitAndForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7)
	model := "gpt-image-2.6-flare"
	account := service.Account{
		ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "fixture", "base_url": "https://provider.invalid", "model_mapping": map[string]any{model: "gpt-image-2.5-flare"}},
	}
	repo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
	prepay := &gpt26PrepayCounter{}
	upstream := &openAIImagesFailoverHTTPUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple, DoneBot: config.DoneBotConfig{Enabled: true}}
	billing := service.NewBillingService(cfg, nil)
	resolver := service.NewModelPricingResolver(nil, billing)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(repo, nil, prepay, nil, nil, nil, nil, cfg, nil, nil, billing, nil, billingCache, upstream, nil, nil, nil, resolver, nil, nil, nil, nil)
	handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	one, two, four := 0.1, 0.2, 0.3
	key := &service.APIKey{ID: 9, UserID: 100, Key: "sk-donebot-fixture", GroupID: &groupID, User: &service.User{ID: 100}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true, RateMultiplier: 1, ImagePrice1K: &one, ImagePrice2K: &two, ImagePrice4K: &four}}
	requestContext := service.WithDoneBotMediaReceipt(context.Background(), uuid.NewString())
	require.NoError(t, gateway.PrepayDoneBotImage(requestContext, key, &account, model, 1))
	require.Equal(t, 1, prepay.calls, "the configured tariff reaches the debit repository")
	prepay.calls = 0
	body := `{"model":"gpt-image-2.6-flare","prompt":"fixture","output_format":"gif"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body)).WithContext(requestContext)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = request
	c.Set(string(middleware2.ContextKeyAPIKey), key)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100})
	c.Set(securityAuditCompletedContextKey, true)
	handler.Images(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "GIF output requires a GPT Image 2.6 model")
	require.Zero(t, prepay.calls)
	require.Empty(t, upstream.calls())
}
