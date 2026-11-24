package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mobilehub"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func doneBotMobileService(db *sql.DB, auth *service.AuthService, settings *service.DoneBotMobileSettings) *mobilehub.Service {
	result := &mobilehub.Service{DB: db, Check: func(ctx context.Context, who mobilehub.Identity) error {
		return auth.CheckMobileSession(ctx, who.Owner, who.Session, who.Version)
	}}
	if settings != nil {
		result.ResolveConfig = settings.RelayConfig
		result.ResolvePush = settings.PushSender
		go result.RunDelivery(context.Background())
	}
	return result
}
func mobileBody(c *gin.Context, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "Invalid mobile request")
		return false
	}
	return true
}
func (h *DoneBotHandler) mobileOwner(c *gin.Context) (int64, bool) {
	c.Header("Cache-Control", "no-store")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	if h.mobile == nil {
		response.Error(c, 503, "Mobile connection service unavailable")
		return 0, false
	}
	return subject.UserID, true
}
func (h *DoneBotHandler) mobileIdentity(c *gin.Context) (mobilehub.Identity, bool) {
	owner, ok := h.mobileOwner(c)
	if !ok {
		return mobilehub.Identity{}, false
	}
	if h.mobileAuth == nil {
		response.Error(c, 503, "Mobile authentication unavailable")
		return mobilehub.Identity{}, false
	}
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		response.Unauthorized(c, "Invalid login session")
		return mobilehub.Identity{}, false
	}
	claims, err := h.mobileAuth.ValidateToken(parts[1])
	if err != nil || claims.UserID != owner || claims.SessionID == "" {
		response.Unauthorized(c, "A current login session is required")
		return mobilehub.Identity{}, false
	}
	who := mobilehub.Identity{Owner: owner, Session: claims.SessionID, Version: claims.TokenVersion}
	if h.mobile.Check == nil {
		response.Error(c, 503, "Mobile authentication unavailable")
		return mobilehub.Identity{}, false
	}
	if response.ErrorFrom(c, h.mobile.Check(c.Request.Context(), who)) {
		return mobilehub.Identity{}, false
	}
	return who, true
}
func (h *DoneBotHandler) MobileWorkspaces(c *gin.Context) {
	who, ok := h.mobileIdentity(c)
	if !ok {
		return
	}
	items, err := h.mobile.Workspaces(c.Request.Context(), who.Owner)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"workspaces": items})
	}
}
func (h *DoneBotHandler) MobileRemoveComputer(c *gin.Context) {
	who, ok := h.mobileIdentity(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, h.mobile.RemoveComputer(c.Request.Context(), who, c.Param("id"))) {
		c.Status(204)
	}
}
func (h *DoneBotHandler) Devices(c *gin.Context) {
	owner, ok := h.mobileOwner(c)
	if !ok {
		return
	}
	out, err := h.mobile.Devices(c.Request.Context(), owner)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) MobilePresence(c *gin.Context) {
	who, ok := h.mobileIdentity(c)
	if !ok {
		return
	}
	var in mobilehub.PhonePresence
	if !mobileBody(c, &in) {
		return
	}
	if !response.ErrorFrom(c, h.mobile.PhonePresence(c.Request.Context(), who, in)) {
		c.Status(204)
	}
}
func (h *DoneBotHandler) RelayNodeHeartbeat(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.mobile == nil {
		response.Error(c, 503, "Relay management unavailable")
		return
	}
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		response.Unauthorized(c, "Relay credential required")
		return
	}
	var in mobilehub.NodeHeartbeat
	if !mobileBody(c, &in) {
		return
	}
	out, err := h.mobile.NodeHeartbeat(c.Request.Context(), c.Param("node"), parts[1], in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) MobileConnection(c *gin.Context) {
	who, ok := h.mobileIdentity(c)
	if !ok {
		return
	}
	var in struct {
		Installation string `json:"installationId"`
	}
	if !mobileBody(c, &in) {
		return
	}
	result, err := h.mobile.Connection(c.Request.Context(), who, c.Param("id"), in.Installation)
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}
func (h *DoneBotHandler) MobileSubscribe(c *gin.Context) {
	who, ok := h.mobileIdentity(c)
	if !ok {
		return
	}
	var in mobilehub.Subscription
	if !mobileBody(c, &in) {
		return
	}
	if !response.ErrorFrom(c, h.mobile.Subscribe(c.Request.Context(), who, in)) {
		c.Status(204)
	}
}
func (h *DoneBotHandler) MobileUnsubscribe(c *gin.Context) {
	who, ok := h.mobileIdentity(c)
	if !ok {
		return
	}
	var in struct {
		Installation string `json:"installationId"`
	}
	if !mobileBody(c, &in) {
		return
	}
	if !response.ErrorFrom(c, h.mobile.Unsubscribe(c.Request.Context(), who, in.Installation)) {
		c.Status(204)
	}
}
func (h *DoneBotHandler) MobilePushStream(c *gin.Context) {
	who, ok := h.mobileIdentity(c)
	if !ok {
		return
	}
	installation := strings.TrimSpace(c.Query("installationId"))
	ctx := c.Request.Context()
	if response.ErrorFrom(c, h.mobile.VerifyStreamDevice(ctx, who, installation)) {
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		response.Error(c, http.StatusInternalServerError, "Streaming is unsupported")
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-store, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetWriteDeadline(time.Time{})

	wake, stopWatch := h.mobile.WatchPush(who.Owner)
	defer stopWatch()

	writeEvent := func(eventName string, id int64, payload any) bool {
		raw, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if id > 0 {
			if _, err = fmt.Fprintf(c.Writer, "id: %d\n", id); err != nil {
				return false
			}
		}
		if _, err = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventName, raw); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !writeEvent("ready", 0, map[string]bool{"ok": true}) {
		return
	}

	drain := func() bool {
		events, err := h.mobile.PullStreamEvents(ctx, who, installation)
		if err != nil {
			if code := infraerrors.Code(err); code == 401 || code == 403 || code == 404 {
				_ = writeEvent("revoked", 0, map[string]string{"reason": "unsubscribed"})
				return false
			}
			return true
		}
		for _, ev := range events {
			if !writeEvent("message", ev.ID, ev) {
				return false
			}
		}
		return true
	}

	if !drain() {
		return
	}

	pollTicker := time.NewTicker(3 * time.Second)
	defer pollTicker.Stop()
	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			if !drain() {
				return
			}
		case <-pollTicker.C:
			if !drain() {
				return
			}
		case <-pingTicker.C:
			if err := h.mobile.VerifyStreamDevice(ctx, who, installation); err != nil {
				if code := infraerrors.Code(err); code == 401 || code == 403 || code == 404 {
					_ = writeEvent("revoked", 0, map[string]string{"reason": "unsubscribed"})
					return
				}
				continue
			}
			if _, err := io.WriteString(c.Writer, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
func (h *DoneBotHandler) MobileDisconnect(c *gin.Context) {
	who, ok := h.mobileIdentity(c)
	if !ok {
		return
	}
	var in struct {
		Installation string `json:"installationId"`
	}
	if !mobileBody(c, &in) {
		return
	}
	if !response.ErrorFrom(c, h.mobile.Disconnect(c.Request.Context(), who, in.Installation)) {
		c.Status(204)
	}
}
func (h *DoneBotHandler) MobileHeartbeat(c *gin.Context) {
	owner, ok := h.mobileOwner(c)
	if !ok {
		return
	}
	var in mobilehub.Heartbeat
	if !mobileBody(c, &in) {
		return
	}
	result, err := h.mobile.Register(c.Request.Context(), owner, c.GetHeader("X-DoneBot-Installation"), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
		h.mobile.Wake()
	}
}
func (h *DoneBotHandler) MobileEvents(c *gin.Context) {
	owner, ok := h.mobileOwner(c)
	if !ok {
		return
	}
	var in struct {
		Events []mobilehub.Event `json:"events"`
	}
	if !mobileBody(c, &in) {
		return
	}
	if !response.ErrorFrom(c, h.mobile.Events(c.Request.Context(), owner, c.GetHeader("X-DoneBot-Installation"), in.Events)) {
		c.Status(204)
		h.mobile.Wake()
	}
}
