package handler

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type doneBotClaimer interface {
	Claim(context.Context, int64, service.DoneBotClaimRequest) (*service.DoneBotClaimResult, error)
	ListForUser(context.Context, int64, pagination.PaginationParams) ([]service.DoneBotInstall, *pagination.PaginationResult, error)
	RotateForUser(context.Context, int64, string) (*service.DoneBotClaimResult, error)
	RevokeForUser(context.Context, int64, string) error
}

type doneBotUserInstall struct {
	ID             string     `json:"id"`
	InstallationID string     `json:"installation_id"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func doneBotUserInstallation(install *service.DoneBotInstall) doneBotUserInstall {
	return doneBotUserInstall{ID: install.InstallationID, InstallationID: install.InstallationID, Name: install.Name, Status: install.Status,
		LastSeenAt: install.LastSeenAt, ExpiresAt: install.ExpiresAt, CreatedAt: install.CreatedAt, UpdatedAt: install.UpdatedAt}
}

type DoneBotHandler struct {
	installs doneBotClaimer
	settings service.SettingRepository
	cfg      *config.Config
	catalog  doneBotCatalogReader
	account  doneBotAccountReader
}

func NewDoneBotHandler(installs *service.DoneBotService, settings service.SettingRepository, cfg *config.Config) *DoneBotHandler {
	return &DoneBotHandler{installs: installs, settings: settings, cfg: cfg}
}

// Claim uses the authenticated session identity; request fields cannot select an owner.
func (h *DoneBotHandler) Claim(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req service.DoneBotClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid installation request")
		return
	}
	result, err := h.installs.Claim(c.Request.Context(), subject.UserID, req)
	if response.ErrorFrom(c, err) {
		return
	}
	// Credentials are only present on the first committed claim. Do not cache
	// this response in browsers or reverse proxies.
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, struct {
		Install    doneBotUserInstall `json:"install"`
		Credential string             `json:"credential,omitempty"`
		GatewayURL string             `json:"gateway_url"`
	}{doneBotUserInstallation(result.Install), result.Credential, h.gatewayURL(c)})
}

func (h *DoneBotHandler) ListInstallations(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, result, err := h.installs.ListForUser(c.Request.Context(), subject.UserID, pagination.PaginationParams{Page: page, PageSize: pageSize})
	if response.ErrorFrom(c, err) {
		return
	}
	installs := make([]doneBotUserInstall, 0, len(items))
	for i := range items {
		installs = append(installs, doneBotUserInstallation(&items[i]))
	}
	c.JSON(http.StatusOK, gin.H{"installations": installs, "total": result.Total, "page": page, "page_size": pageSize})
}

func doneBotInstallationIdentity(c *gin.Context) (int64, string, bool) {
	c.Header("Cache-Control", "no-store")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return 0, "", false
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		response.BadRequest(c, "Invalid installation ID")
		return 0, "", false
	}
	return subject.UserID, id.String(), true
}

func (h *DoneBotHandler) RotateInstallation(c *gin.Context) {
	userID, id, ok := doneBotInstallationIdentity(c)
	if !ok {
		return
	}
	result, err := h.installs.RotateForUser(c.Request.Context(), userID, id)
	if response.ErrorFrom(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"install": doneBotUserInstallation(result.Install), "credential": result.Credential, "gateway_url": h.gatewayURL(c)})
}

func (h *DoneBotHandler) RevokeInstallation(c *gin.Context) {
	userID, id, ok := doneBotInstallationIdentity(c)
	if !ok {
		return
	}
	if response.ErrorFrom(c, h.installs.RevokeForUser(c.Request.Context(), userID, id)) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *DoneBotHandler) gatewayURL(c *gin.Context) string {
	if h.settings != nil {
		if value, err := h.settings.GetValue(c.Request.Context(), service.SettingKeyAPIBaseURL); err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimRight(strings.TrimSpace(value), "/")
		}
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	} else if forwarded := trustedForwardedScheme(c, h.cfg); forwarded != "" {
		scheme = forwarded
	}
	host := strings.TrimSpace(c.Request.Host)
	if host == "" || strings.ContainsAny(host, "\r\n/@") {
		return ""
	}
	parsed, err := url.Parse("//" + host)
	if err != nil || parsed.Host != host || parsed.Path != "" || parsed.User != nil || parsed.Hostname() == "" {
		return ""
	}
	return scheme + "://" + host
}

func trustedForwardedScheme(c *gin.Context, cfg *config.Config) string {
	if c == nil || cfg == nil || !cfg.Server.TrustedProxiesConfigured {
		return ""
	}
	remoteHost, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return ""
	}
	remote, err := netip.ParseAddr(remoteHost)
	if err != nil {
		return ""
	}
	trusted := false
	for _, value := range cfg.Server.TrustedProxies {
		value = strings.TrimSpace(value)
		if prefix, parseErr := netip.ParsePrefix(value); parseErr == nil && prefix.Contains(remote) {
			trusted = true
			break
		}
		if address, parseErr := netip.ParseAddr(value); parseErr == nil && address == remote {
			trusted = true
			break
		}
	}
	if !trusted {
		return ""
	}
	proto := strings.ToLower(strings.TrimSpace(strings.SplitN(c.GetHeader("X-Forwarded-Proto"), ",", 2)[0]))
	if proto == "https" || proto == "http" {
		return proto
	}
	return ""
}
