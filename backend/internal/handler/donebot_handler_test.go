package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type doneBotClaimStub struct {
	userID         int64
	req            service.DoneBotClaimRequest
	result         *service.DoneBotClaimResult
	calls          int
	installationID string
	err            error
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
func (s *doneBotClaimStub) ListForUser(_ context.Context, userID int64, params pagination.PaginationParams) ([]service.DoneBotInstall, *pagination.PaginationResult, error) {
	s.calls++
	s.userID = userID
	return []service.DoneBotInstall{*s.result.Install}, &pagination.PaginationResult{Total: 1, Page: params.Page, PageSize: params.PageSize}, s.err
}
func (s *doneBotClaimStub) RotateForUser(_ context.Context, userID int64, id string) (*service.DoneBotClaimResult, error) {
	s.calls++
	s.userID, s.installationID = userID, id
	return s.result, s.err
}
func (s *doneBotClaimStub) RevokeForUser(_ context.Context, userID int64, id string) error {
	s.calls++
	s.userID, s.installationID = userID, id
	return s.err
}

func TestDoneBotClaimUsesSessionUserAndReturnsCredentialOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &doneBotClaimStub{result: &service.DoneBotClaimResult{
		Install:    &service.DoneBotInstall{ID: 12, InstallationID: "a35372c0-39ba-41c3-b6b3-632032ba7b04", Name: "我的 Mac", Status: "active", UserID: 7, APIKeyID: 9, GroupID: 11, Fingerprint: "private-fingerprint"},
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
		install, ok := body["install"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, svc.result.Install.InstallationID, install["id"])
		for _, hidden := range []string{"user_id", "api_key_id", "group_id", "fingerprint"} {
			require.NotContains(t, install, hidden)
		}
	}
	require.Equal(t, int64(7), svc.userID)
	require.Equal(t, "我的 Mac", svc.req.InstallName)
	require.Equal(t, "device", svc.req.Fingerprint)
}

func TestDoneBotInstallationRoutesUseAuthenticatedOwnerAndSafeDTO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := "a35372c0-39ba-41c3-b6b3-632032ba7b04"
	svc := &doneBotClaimStub{result: &service.DoneBotClaimResult{Install: &service.DoneBotInstall{ID: 1, InstallationID: id, UserID: 7, APIKeyID: 8, GroupID: 9, Name: "Mac", Status: "active"}, Credential: "sk-donebot-once"}}
	h := &DoneBotHandler{installs: svc}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
	r.GET("/installations", h.ListInstallations)
	r.POST("/installations/:id/rotate", h.RotateInstallation)
	r.DELETE("/installations/:id", h.RevokeInstallation)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/installations?user_id=999", http.StatusOK},
		{http.MethodPost, "/installations/" + id + "/rotate?user_id=999", http.StatusOK},
		{http.MethodDelete, "/installations/" + id + "?user_id=999", http.StatusNoContent},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, tc.status, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.EqualValues(t, 7, svc.userID)
		for _, hidden := range []string{"user_id", "api_key_id", "group_id", "fingerprint"} {
			require.NotContains(t, w.Body.String(), hidden)
		}
		if tc.method == http.MethodGet {
			require.NotContains(t, w.Body.String(), "credential")
			require.NotContains(t, w.Body.String(), "sk-donebot-")
		}
	}
	require.Equal(t, id, svc.installationID)
}

func TestDoneBotInstallationManagementRequiresSessionAndRejectsMalformedUUID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, authenticated := range []bool{false, true} {
		svc := &doneBotClaimStub{}
		h := &DoneBotHandler{installs: svc}
		r := gin.New()
		if authenticated {
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
		}
		r.POST("/installations/:id/rotate", h.RotateInstallation)
		r.DELETE("/installations/:id", h.RevokeInstallation)
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			path := "/installations/not-a-uuid"
			if method == http.MethodPost {
				path += "/rotate"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			want := http.StatusUnauthorized
			if authenticated {
				want = http.StatusBadRequest
			}
			require.Equal(t, want, w.Code)
		}
		require.Zero(t, svc.calls)
	}
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
