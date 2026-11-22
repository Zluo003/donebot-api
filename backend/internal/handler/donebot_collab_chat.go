package handler

import (
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/donebotcollab"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) CollabChatSummaries(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.ChatSummaries(c.Request.Context(), actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabChatMessagesList(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var afterID, beforeID int64
	var limit int
	if raw := strings.TrimSpace(c.Query("after_id")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			afterID = v
		}
	}
	if raw := strings.TrimSpace(c.Query("before_id")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			beforeID = v
		}
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	out, err := svc.ListChatMessages(c.Request.Context(), actor, c.Param("scope_type"), c.Param("target_id"), afterID, beforeID, limit)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabChatFilesList(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	out, err := svc.ListChatFiles(c.Request.Context(), actor, c.Param("scope_type"), c.Param("target_id"), limit)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabChatMessageSend(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !strictCollabBody(c, 64<<10, &in) {
		return
	}
	out, err := svc.SendChatMessage(c.Request.Context(), actor, c.Param("scope_type"), c.Param("target_id"), in.Body)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabChatFileUpload(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	if c.Request.ContentLength > donebotcollab.MaxChatFileBytes {
		response.Error(c, http.StatusRequestEntityTooLarge, "发送的文件不能超过 100 MB。")
		return
	}
	fileName := c.Query("name")
	caption := c.Query("body")
	contentType := strings.TrimSpace(c.GetHeader("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	limitedBody := http.MaxBytesReader(c.Writer, c.Request.Body, donebotcollab.MaxChatFileBytes)
	out, err := svc.SendChatFile(
		c.Request.Context(),
		actor,
		c.Param("scope_type"),
		c.Param("target_id"),
		fileName,
		contentType,
		caption,
		limitedBody,
		c.Request.ContentLength,
	)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabChatReadMark(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		LastReadID int64 `json:"last_read_id"`
	}
	if c.Request.ContentLength != 0 {
		if !strictCollabBody(c, 4<<10, &in) {
			return
		}
	}
	id, err := svc.MarkChatRead(c.Request.Context(), actor, c.Param("scope_type"), c.Param("target_id"), in.LastReadID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"last_read_id": id})
	}
}

func (h *DoneBotHandler) CollabChatFileDownload(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	f, meta, err := svc.OpenChatFile(c.Request.Context(), actor, c.Param("file_id"))
	if response.ErrorFrom(c, err) {
		return
	}
	defer f.Close()

	contentType := strings.TrimSpace(meta.FileMime)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disp := "attachment"
	if c.Query("inline") == "1" && donebotcollab.IsSafeInlineChatMime(contentType) {
		disp = "inline"
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": meta.FileName}))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, max-age=3600")
	if meta.FileSize >= 0 {
		c.Header("Content-Length", strconv.FormatInt(meta.FileSize, 10))
	}
	if meta.FileSha256 != "" {
		c.Header("X-File-Sha256", meta.FileSha256)
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, io.LimitReader(f, donebotcollab.MaxChatFileBytes))
}
