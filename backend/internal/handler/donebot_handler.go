package handler

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type doneBotClaimer interface {
	Claim(context.Context, int64, service.DoneBotClaimRequest) (*service.DoneBotClaimResult, error)
}

type DoneBotHandler struct {
	installs doneBotClaimer
	settings service.SettingRepository
	cfg      *config.Config
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
		Install    *service.DoneBotInstall `json:"install"`
		Credential string                  `json:"credential,omitempty"`
		GatewayURL string                  `json:"gateway_url"`
	}{result.Install, result.Credential, h.gatewayURL(c)})
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
