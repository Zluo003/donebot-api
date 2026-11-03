package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type doneBotCatalogHandlerStub struct {
	result         *service.DoneBotCatalogResult
	err            error
	calls          int
	userID         int64
	installationID string
}

func (s *doneBotCatalogHandlerStub) Models(_ context.Context, userID int64, installationID string) (*service.DoneBotCatalogResult, error) {
	s.calls++
	s.userID, s.installationID = userID, installationID
	return s.result, s.err
}

type doneBotAccountHandlerStub struct {
	account        *service.DoneBotAccount
	receipt        *service.DoneBotReceipt
	err            error
	calls          int
	userID         int64
	installationID string
	receiptID      string
}

func (s *doneBotAccountHandlerStub) Account(_ context.Context, userID int64, installationID string) (*service.DoneBotAccount, error) {
	s.calls++
	s.userID, s.installationID = userID, installationID
	return s.account, s.err
}

func (s *doneBotAccountHandlerStub) Receipt(_ context.Context, userID int64, installationID, receiptID string) (*service.DoneBotReceipt, error) {
	s.calls++
	s.userID, s.installationID, s.receiptID = userID, installationID, receiptID
	return s.receipt, s.err
}

func TestDoneBotCatalogChecksInstallationScopeBeforeConditionalResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &doneBotCatalogHandlerStub{result: &service.DoneBotCatalogResult{CatalogScopeID: "scope-owner", CatalogVersion: "version", ETag: `"scoped-version"`}}
	h := &DoneBotHandler{catalog: stub}
	router := gin.New()
	userID := int64(7)
	router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID}) })
	router.GET("/models", h.Models)
	for _, tc := range []struct {
		name, etag string
		status     int
	}{
		{"fresh", "", http.StatusOK},
		{"same scope", `"scoped-version"`, http.StatusNotModified},
		{"weak validator", `W/"scoped-version"`, http.StatusNotModified},
		{"wildcard", "*", http.StatusNotModified},
		{"different version", `"old-version"`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/models?user_id=999", nil)
			request.Header.Set("X-DoneBot-Installation", "installation-owner")
			request.Header.Set("If-None-Match", tc.etag)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			require.Equal(t, `"scoped-version"`, response.Header().Get("ETag"))
			require.Equal(t, "private, no-cache", response.Header().Get("Cache-Control"))
			require.Equal(t, "Authorization, X-DoneBot-Installation", response.Header().Get("Vary"))
			require.EqualValues(t, 7, stub.userID)
			require.Equal(t, "installation-owner", stub.installationID)
		})
	}
	require.Equal(t, 5, stub.calls)
	userID = 8
	stub.err = service.ErrDoneBotInstallNotFound
	for _, etag := range []string{`"scoped-version"`, "*"} {
		request := httptest.NewRequest(http.MethodGet, "/models", nil)
		request.Header.Set("X-DoneBot-Installation", "installation-owner")
		request.Header.Set("If-None-Match", etag)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		require.Empty(t, response.Header().Get("ETag"))
		require.EqualValues(t, 8, stub.userID)
	}
	require.Equal(t, 7, stub.calls)
}

func TestDoneBotCatalogAccountAndReceiptRequireSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, subject := range []*middleware.AuthSubject{nil, {UserID: 0}} {
		catalog := &doneBotCatalogHandlerStub{}
		account := &doneBotAccountHandlerStub{}
		h := &DoneBotHandler{catalog: catalog, account: account}
		router := gin.New()
		if subject != nil {
			router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), *subject) })
		}
		router.GET("/models", h.Models)
		router.GET("/account", h.Account)
		router.GET("/receipts/:id", h.Receipt)
		for _, path := range []string{"/models", "/account", "/receipts/receipt-id"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("If-None-Match", "*")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
			require.Empty(t, response.Header().Get("ETag"))
		}
		require.Zero(t, catalog.calls)
		require.Zero(t, account.calls)
	}
}

func TestDoneBotAccountAndReceiptForwardSessionAndInstallation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &doneBotAccountHandlerStub{account: &service.DoneBotAccount{ID: "7", Currency: "CNY", BalanceCents: json.Number("100"), FrozenCents: json.Number("0"), Usage: service.DoneBotAccountUsage{ActualCents: json.Number("0")}}, receipt: &service.DoneBotReceipt{ReceiptID: "receipt-id", Currency: "CNY", ActualCents: json.Number("0")}}
	h := &DoneBotHandler{account: stub}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
	router.GET("/account", h.Account)
	router.GET("/receipts/:id", h.Receipt)
	for _, path := range []string{"/account?user_id=999", "/receipts/receipt-id?user_id=999"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-DoneBot-Installation", "installation-owner")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		require.EqualValues(t, 7, stub.userID)
		require.Equal(t, "installation-owner", stub.installationID)
	}
	require.Equal(t, "receipt-id", stub.receiptID)
}

func TestDoneBotPendingReceiptReturnsRetryAfterWithoutCaching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &doneBotAccountHandlerStub{err: service.ErrUsageLogNotFound}
	h := &DoneBotHandler{account: stub}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
	router.GET("/receipts/:id", h.Receipt)
	request := httptest.NewRequest(http.MethodGet, "/receipts/not-yet-persisted", nil)
	request.Header.Set("X-DoneBot-Installation", "installation-owner")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	require.Equal(t, "3", response.Header().Get("Retry-After"))
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, "not-yet-persisted", stub.receiptID)
}
