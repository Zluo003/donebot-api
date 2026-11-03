package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type doneBotClaimStub struct {
	userID int64
	req    service.DoneBotClaimRequest
	result *service.DoneBotClaimResult
	calls  int
}

type doneBotGatewayURLSettings struct {
	service.SettingRepository
	value string
	err   error
}

func (s doneBotGatewayURLSettings) GetValue(context.Context, string) (string, error) {
	return s.value, s.err
}

func (s *doneBotClaimStub) Claim(_ context.Context, userID int64, req service.DoneBotClaimRequest) (*service.DoneBotClaimResult, error) {
	s.calls++
	s.userID, s.req = userID, req
	return s.result, nil
}

func TestDoneBotClaimUsesSessionUserAndReturnsCredentialOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &doneBotClaimStub{result: &service.DoneBotClaimResult{
		Install:    &service.DoneBotInstall{ID: 12, Name: "我的 Mac", Status: "active"},
		Credential: "sk-donebot-secret",
	}}
	cfg := &config.Config{}
	cfg.Server.FrontendURL = "https://frontend.example.com/"
	h := &DoneBotHandler{installs: svc, cfg: cfg}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
	})
	r.POST("/claim", h.Claim)
	for _, initial := range []bool{true, false} {
		if !initial {
			svc.result.Credential = ""
		}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"installation_id":"a35372c0-39ba-41c3-b6b3-632032ba7b04","install_name":"我的 Mac","fingerprint":"device","user_id":999}`))
		req.Header.Set("Content-Type", "application/json")
		req.Host = "gateway.example.com"
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, "http://gateway.example.com", body["gateway_url"])
		_, hasCredential := body["credential"]
		require.Equal(t, initial, hasCredential)
		require.Equal(t, float64(12), body["install"].(map[string]any)["id"])
	}
	require.Equal(t, int64(7), svc.userID)
	require.Equal(t, "我的 Mac", svc.req.InstallName)
	require.Equal(t, "device", svc.req.Fingerprint)
}

func TestDoneBotGatewayURLUsesConfiguredAPIBaseAndTrustedForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var got string
	router.GET("/url", func(c *gin.Context) {
		got = (&DoneBotHandler{settings: doneBotGatewayURLSettings{value: "  https://api.example.com/v1/  "}}).gatewayURL(c)
	})
	request := httptest.NewRequest(http.MethodGet, "/url", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, "https://api.example.com/v1", got)

	cfg := &config.Config{}
	cfg.Server.TrustedProxiesConfigured = true
	cfg.Server.TrustedProxies = []string{"10.0.0.0/8"}
	router = gin.New()
	router.GET("/url", func(c *gin.Context) { got = (&DoneBotHandler{cfg: cfg}).gatewayURL(c) })
	request = httptest.NewRequest(http.MethodGet, "/url", nil)
	request.Host = "gateway.example.com"
	request.RemoteAddr = "10.2.3.4:8080"
	request.Header.Set("X-Forwarded-Proto", "https")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, "https://gateway.example.com", got)

	request = httptest.NewRequest(http.MethodGet, "/url", nil)
	request.Host = "gateway.example.com"
	request.RemoteAddr = "203.0.113.4:8080"
	request.Header.Set("X-Forwarded-Proto", "https")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, "http://gateway.example.com", got, "untrusted forwarded headers must be ignored")
}

func TestDoneBotClaimRejectsMissingSessionAndMalformedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, authenticated := range []bool{false, true} {
		svc := &doneBotClaimStub{}
		h := &DoneBotHandler{installs: svc}
		r := gin.New()
		if authenticated {
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
		}
		r.POST("/claim", h.Claim)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"installation_id":`)))
		want := http.StatusUnauthorized
		if authenticated {
			want = http.StatusBadRequest
		}
		require.Equal(t, want, w.Code)
		require.Zero(t, svc.calls)
	}
}
