package handler

import (
	"io"
	"mime"
	"net/http"
	"path/filepath"
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
		Body          string   `json:"body"`
		MentionBotIDs []string `json:"mention_bot_ids,omitempty"`
	}
	if !strictCollabBody(c, 64<<10, &in) {
		return
	}
	out, err := svc.SendChatMessage(c.Request.Context(), actor, c.Param("scope_type"), c.Param("target_id"), in.Body, in.MentionBotIDs)
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
	if contentType == "" || strings.EqualFold(contentType, "application/octet-stream") {
		if guessed := strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(meta.FileName)))); guessed != "" {
			contentType = guessed
		}
	}
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
	if meta.FileSha256 != "" {
		c.Header("X-File-Sha256", meta.FileSha256)
	}
	if stat, statErr := f.Stat(); statErr == nil && stat.Size() <= donebotcollab.MaxChatFileBytes {
		http.ServeContent(c.Writer, c.Request, meta.FileName, stat.ModTime(), f)
		return
	}
	if meta.FileSize >= 0 {
		c.Header("Content-Length", strconv.FormatInt(meta.FileSize, 10))
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, io.LimitReader(f, donebotcollab.MaxChatFileBytes))
}

// --- Team Avatar & Friend Groups ---

func (h *DoneBotHandler) CollabTeamAvatarSet(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		AvatarURL string `json:"avatar_url"`
	}
	if !strictCollabBody(c, 1<<20, &in) {
		return
	}
	avatar, err := svc.SetTeamAvatar(c.Request.Context(), actor, c.Param("team"), in.AvatarURL)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"team_id": c.Param("team"), "avatar_url": avatar})
	}
}

func (h *DoneBotHandler) CollabGroupsList(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.ListGroups(c.Request.Context(), actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabGroupCreate(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		Name      string  `json:"name"`
		MemberIDs []int64 `json:"member_ids"`
	}
	if !strictCollabBody(c, 32<<10, &in) {
		return
	}
	out, err := svc.CreateGroup(c.Request.Context(), actor, in.Name, in.MemberIDs)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabGroupRename(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !strictCollabBody(c, 8<<10, &in) {
		return
	}
	out, err := svc.RenameGroup(c.Request.Context(), actor, c.Param("id"), in.Name)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabGroupAvatarSet(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		AvatarURL string `json:"avatar_url"`
	}
	if !strictCollabBody(c, 1<<20, &in) {
		return
	}
	avatar, err := svc.SetGroupAvatar(c.Request.Context(), actor, c.Param("id"), in.AvatarURL)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"group_id": c.Param("id"), "avatar_url": avatar})
	}
}

func (h *DoneBotHandler) CollabGroupMembersAdd(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		MemberIDs []int64 `json:"member_ids"`
	}
	if !strictCollabBody(c, 32<<10, &in) {
		return
	}
	out, err := svc.AddGroupMembers(c.Request.Context(), actor, c.Param("id"), in.MemberIDs)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabGroupLeave(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, svc.LeaveGroup(c.Request.Context(), actor, c.Param("id"))) {
		response.Success(c, gin.H{"ok": true})
	}
}

// --- Group / Team Chat Bots & Local Dispatch ---

func (h *DoneBotHandler) CollabChatBotsList(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.ListChatBots(c.Request.Context(), actor, c.Param("scope_type"), c.Param("target_id"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabChatBotAdd(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in donebotcollab.AddChatBotInput
	if !strictCollabBody(c, 1<<20, &in) {
		return
	}
	out, err := svc.AddChatBot(c.Request.Context(), actor, c.Param("scope_type"), c.Param("target_id"), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabChatBotGrantsUpdate(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in donebotcollab.UpdateChatBotGrantsInput
	if !strictCollabBody(c, 32<<10, &in) {
		return
	}
	out, err := svc.UpdateChatBotGrants(c.Request.Context(), actor, c.Param("scope_type"), c.Param("target_id"), c.Param("bot_id"), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabChatBotRemove(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, svc.RemoveChatBot(c.Request.Context(), actor, c.Param("scope_type"), c.Param("target_id"), c.Param("bot_id"))) {
		response.Success(c, gin.H{"ok": true})
	}
}

func (h *DoneBotHandler) CollabChatBotClaim(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		InstallationID string `json:"installation_id"`
	}
	if c.Request.ContentLength != 0 {
		if !strictCollabBody(c, 8<<10, &in) {
			return
		}
	}
	installID := in.InstallationID
	if installID == "" {
		installID = c.GetHeader("X-DoneBot-Installation")
	}
	out, err := svc.ClaimBotInvocation(c.Request.Context(), actor, installID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"invocation": out})
	}
}

func (h *DoneBotHandler) CollabChatBotReply(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		Body  string `json:"body"`
		Error string `json:"error"`
	}
	if !strictCollabBody(c, 128<<10, &in) {
		return
	}
	out, err := svc.CompleteBotInvocation(c.Request.Context(), actor, c.Param("id"), in.Body, in.Error)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"message": out})
	}
}
