package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/mobilehub"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func doneBotMobileService(db *sql.DB, auth *service.AuthService) *mobilehub.Service {
	result := &mobilehub.Service{DB: db, Config: mobilehub.EnvironmentConfig(), Check: func(ctx context.Context, who mobilehub.Identity) error {
		return auth.CheckMobileSession(ctx, who.Owner, who.Session, who.Version)
	}}
	if sender := mobilehub.EnvironmentAPNs(); sender != nil {
		result.Push = sender
	}
	if result.Config != nil && result.Push != nil {
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
