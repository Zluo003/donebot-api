//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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

func (repo *gpt26PrepayCounter) BindDoneBotMediaTask(context.Context, string, int64, string) error {
	return nil
}
func (repo *gpt26PrepayCounter) MarkDoneBotMediaRejected(context.Context, string) error { return nil }
func (repo *gpt26PrepayCounter) LookupDoneBotMediaPrecharge(context.Context, string, int64, string) (*service.DoneBotMediaPrecharge, error) {
	return nil, nil
}

func TestDoneBotGPTImage26SupplierNameCannotOverridePublicGIFCapability(t *testing.T) {
	handler, _, key, prepay, upstream := gifAliasHandler(t, "gpt-image-2.6-flare", "arbitrary-provider-name", false)
	c, recorder, cancel := gifAliasRequest(t, "gpt-image-2.6-flare", "generations", "json", key)
	defer cancel()
	handler.Images(c)
	require.Equal(t, 1, upstream.requests, recorder.Body.String())
	require.Equal(t, 1, prepay.calls)
	require.Equal(t, "arbitrary-provider-name", upstream.body["model"])
}

type gifAliasCaptureUpstream struct {
	service.HTTPUpstream
	body             map[string]string
	file             []byte
	path             string
	requests         int
	prepay           *gpt26PrepayCounter
	prepayAtDispatch int
}

func (u *gifAliasCaptureUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.requests++
	if u.prepay != nil {
		u.prepayAtDispatch = u.prepay.calls
	}
	u.path = req.URL.Path
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.body = map[string]string{}
	media, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err == nil && media == "multipart/form-data" {
		reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(part)
			if err != nil {
				return nil, err
			}
			if part.FileName() == "" {
				u.body[part.FormName()] = string(data)
			} else {
				u.file = data
			}
		}
	} else {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		for name, value := range fields {
			var decoded string
			if json.Unmarshal(value, &decoded) == nil {
				u.body[name] = decoded
			} else {
				u.body[name] = string(value)
			}
		}
	}
	return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"offline GIF alias capture reached upstream"}}`))}, nil
}

type gifAliasChannelRepo struct {
	service.ChannelRepository
	model, mapped string
}

func (r *gifAliasChannelRepo) ListAll(context.Context) ([]service.Channel, error) {
	return []service.Channel{{ID: 1, Status: service.StatusActive, GroupIDs: []int64{7}, ModelMapping: map[string]map[string]string{service.PlatformOpenAI: {r.model: r.mapped}}}}, nil
}
func (r *gifAliasChannelRepo) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{7: service.PlatformOpenAI}, nil
}

func gifAliasHandler(t *testing.T, model, supplier string, channel bool) (*OpenAIGatewayHandler, *service.OpenAIGatewayService, *service.APIKey, *gpt26PrepayCounter, *gifAliasCaptureUpstream) {
	t.Helper()
	credentials := map[string]any{"api_key": "fixture", "base_url": "https://provider.invalid"}
	var channelService *service.ChannelService
	if channel {
		channelService = service.NewChannelService(&gifAliasChannelRepo{model: model, mapped: supplier}, nil, nil, nil, nil)
	} else {
		credentials["model_mapping"] = map[string]any{model: supplier}
	}
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: credentials}
	repo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
	prepay := &gpt26PrepayCounter{}
	upstream := &gifAliasCaptureUpstream{prepay: prepay}
	cfg := &config.Config{RunMode: config.RunModeSimple, DoneBot: config.DoneBotConfig{Enabled: true}}
	billing := service.NewBillingService(cfg, nil)
	resolver := service.NewModelPricingResolver(nil, billing)
	cache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(cache.Stop)
	gateway := service.NewOpenAIGatewayService(repo, nil, prepay, nil, nil, nil, nil, cfg, nil, nil, billing, nil, cache, upstream, nil, nil, nil, resolver, channelService, nil, nil, nil)
	handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), cache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	groupID := int64(7)
	one, two, four := 0.1, 0.2, 0.3
	key := &service.APIKey{ID: 9, UserID: 100, Key: "sk-donebot-fixture", GroupID: &groupID, User: &service.User{ID: 100}, Group: &service.Group{ID: 7, Platform: service.PlatformOpenAI, AllowImageGeneration: true, RateMultiplier: 1, ImagePrice1K: &one, ImagePrice2K: &two, ImagePrice4K: &four}}
	requestCtx := service.WithDoneBotMediaReceipt(context.Background(), uuid.NewString())
	require.NoError(t, gateway.PrepayDoneBotImage(requestCtx, key, &account, model, 1))
	require.Equal(t, 1, prepay.calls, "fixture verifies tariff reaches atomic debit repository")
	prepay.calls = 0
	return handler, gateway, key, prepay, upstream
}

func gifAliasRequest(t *testing.T, model, endpoint, transport string, key *service.APIKey) (*gin.Context, *httptest.ResponseRecorder, context.CancelFunc) {
	t.Helper()
	fields := map[string]string{"model": model, "prompt": "fixture waving cat animation", "output_format": "gif", "size": "512x512", "quality": "high", "background": "opaque", "frame_count": "9", "frame_duration_ms": "200", "n": "1"}
	var body []byte
	contentType := "application/json"
	if transport == "multipart" {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		for name, value := range fields {
			require.NoError(t, writer.WriteField(name, value))
		}
		if endpoint == "edits" {
			part, err := writer.CreateFormFile("image", "reference.png")
			require.NoError(t, err)
			_, err = part.Write([]byte("fixture static reference bytes"))
			require.NoError(t, err)
		}
		require.NoError(t, writer.Close())
		body = buffer.Bytes()
		contentType = writer.FormDataContentType()
	} else {
		payload := map[string]any{}
		for name, value := range fields {
			if name == "frame_count" || name == "frame_duration_ms" || name == "n" {
				number, err := strconv.Atoi(value)
				require.NoError(t, err)
				payload[name] = number
			} else {
				payload[name] = value
			}
		}
		if endpoint == "edits" {
			payload["images"] = []any{map[string]any{"image_url": "https://assets.example.test/reference.png"}}
		}
		var err error
		body, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	receiptCtx := service.WithDoneBotMediaReceipt(context.Background(), uuid.NewString())
	request := httptest.NewRequest(http.MethodPost, "/v1/images/"+endpoint+"/async", bytes.NewReader(body)).WithContext(receiptCtx)
	request.Header.Set("Content-Type", contentType)
	parent, _ := gin.CreateTestContext(httptest.NewRecorder())
	parent.Request = request
	parent.Set(string(middleware2.ContextKeyAPIKey), key)
	parent.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100})
	parent.Set(securityAuditCompletedContextKey, true)
	return newAsyncImageContext(parent, body, 10*time.Second)
}

func TestDoneBotGPTImage26GIFSupplierAliasThroughAsyncCloneAndHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, channel := range []bool{false, true} {
			for _, endpoint := range []string{"generations", "edits"} {
				for _, transport := range []string{"json", "multipart"} {
					t.Run(model+"/channel="+strconv.FormatBool(channel)+"/"+endpoint+"/"+transport, func(t *testing.T) {
						supplier := "gpt-image-supplier-alias"
						handler, _, key, prepay, upstream := gifAliasHandler(t, model, supplier, channel)
						taskCtx, recorder, cancel := gifAliasRequest(t, model, endpoint, transport, key)
						defer cancel()
						handler.Images(taskCtx)
						require.Equal(t, 1, upstream.requests, recorder.Body.String())
						require.Equal(t, 1, prepay.calls, "exactly one atomic precharge before upstream")
						require.Equal(t, 1, upstream.prepayAtDispatch, "supplier request starts only after atomic precharge")
						require.Equal(t, "/v1/images/"+endpoint, upstream.path, "client async suffix must not reach supplier")
						require.Equal(t, supplier, upstream.body["model"])
						require.Equal(t, "gif", upstream.body["output_format"])
						require.Equal(t, "high", upstream.body["complexity"], "public 2.6 keeps fixed complexity through supplier alias")
						require.Equal(t, "512x512", upstream.body["size"])
						require.Equal(t, "9", upstream.body["frame_count"])
						require.Equal(t, "200", upstream.body["frame_duration_ms"])
						if endpoint == "edits" && transport == "multipart" {
							require.Equal(t, []byte("fixture static reference bytes"), upstream.file)
						}
						if endpoint == "edits" && transport == "json" {
							require.JSONEq(t, `[{"image_url":"https://assets.example.test/reference.png"}]`, upstream.body["images"])
						}
						require.Contains(t, recorder.Body.String(), "offline GIF alias capture reached upstream")
					})
				}
			}
		}
	}
}

func TestDoneBotGPTImage26GIFSupplierNamesRemainOpaque(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, supplier := range []string{"gpt-image-2", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst", "gpt-image-2.5-flare-2026-09-08", "gemini-3-pro-image"} {
			for _, channel := range []bool{false, true} {
				t.Run(model+"/"+supplier+"/channel="+strconv.FormatBool(channel), func(t *testing.T) {
					handler, _, key, prepay, upstream := gifAliasHandler(t, model, supplier, channel)
					taskCtx, recorder, cancel := gifAliasRequest(t, model, "generations", "json", key)
					defer cancel()
					handler.Images(taskCtx)
					require.Equal(t, 1, prepay.calls, recorder.Body.String())
					require.Equal(t, 1, upstream.requests)
					require.Equal(t, supplier, upstream.body["model"])
				})
			}
		}
	}
}

func TestDoneBotGPTImage26GIFUnknownPublicAndSupplierRejectsBeforeDebitAndForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, channel := range []bool{false, true} {
		t.Run("channel="+strconv.FormatBool(channel), func(t *testing.T) {
			model := "gpt-image-public-alias"
			handler, _, key, prepay, upstream := gifAliasHandler(t, model, "gpt-image-supplier-alias", channel)
			taskCtx, recorder, cancel := gifAliasRequest(t, model, "generations", "json", key)
			defer cancel()
			handler.Images(taskCtx)
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			require.Zero(t, prepay.calls)
			require.Zero(t, upstream.requests)
		})
	}
}

func TestDoneBotGPTImage26GIFCompositeEvidenceSurvivesRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, knownModel := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, routeCase := range []struct {
			name, public, resolved string
			allowed                bool
		}{
			{name: "known-public-to-route-alias", public: knownModel, resolved: "gpt-image-route-alias", allowed: true},
			{name: "unknown-public-to-known-model", public: "gpt-image-public-alias", resolved: knownModel, allowed: true},
			{name: "legacy-public-to-known-model", public: "gpt-image-2.5-flare", resolved: knownModel, allowed: false},
		} {
			for _, endpoint := range []string{"generations", "edits"} {
				for _, transport := range []string{"json", "multipart"} {
					t.Run(knownModel+"/"+routeCase.name+"/"+endpoint+"/"+transport, func(t *testing.T) {
						supplier := "gpt-image-supplier-alias"
						handler, _, key, prepay, upstream := gifAliasHandler(t, routeCase.resolved, supplier, false)
						key.Group.Platform = service.PlatformComposite
						bodyModel := routeCase.public
						if transport == "json" {
							bodyModel = routeCase.resolved
						}
						taskCtx, recorder, cancel := gifAliasRequest(t, bodyModel, endpoint, transport, key)
						defer cancel()
						taskCtx.Request = taskCtx.Request.WithContext(service.WithCompositeRouteDecision(taskCtx.Request.Context(), service.CompositeRouteDecision{Matched: true, GroupID: 7, TargetPlatform: service.PlatformOpenAI, PublicModel: routeCase.public, UpstreamModel: routeCase.resolved}))
						handler.Images(taskCtx)
						if !routeCase.allowed {
							require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
							require.Zero(t, prepay.calls)
							require.Zero(t, upstream.requests)
							return
						}
						require.Equal(t, 1, upstream.requests, recorder.Body.String())
						require.Equal(t, 1, prepay.calls)
						require.Equal(t, 1, upstream.prepayAtDispatch)
						require.Equal(t, "/v1/images/"+endpoint, upstream.path)
						require.Equal(t, supplier, upstream.body["model"])
						require.Equal(t, "high", upstream.body["complexity"])
						require.Equal(t, "gif", upstream.body["output_format"])
						require.Equal(t, "9", upstream.body["frame_count"])
						require.Equal(t, "200", upstream.body["frame_duration_ms"])
						if endpoint == "edits" && transport == "multipart" {
							require.Equal(t, []byte("fixture static reference bytes"), upstream.file)
						}
						if endpoint == "edits" && transport == "json" {
							require.JSONEq(t, `[{"image_url":"https://assets.example.test/reference.png"}]`, upstream.body["images"])
						}
					})
				}
			}
		}
	}
}
