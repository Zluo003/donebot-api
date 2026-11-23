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

// --- Team Channels & Member Job Titles ---

func (h *DoneBotHandler) CollabTeamChannelsList(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	includeArchived := c.Query("include_archived") == "1" || strings.EqualFold(c.Query("include_archived"), "true")
	out, err := svc.ListTeamChannels(c.Request.Context(), actor, c.Param("team"), includeArchived)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamChannelCreate(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in donebotcollab.CreateTeamChannelInput
	if !strictCollabBody(c, 32<<10, &in) {
		return
	}
	out, err := svc.CreateTeamChannel(c.Request.Context(), actor, c.Param("team"), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamChannelUpdate(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in donebotcollab.UpdateTeamChannelInput
	if !strictCollabBody(c, 32<<10, &in) {
		return
	}
	out, err := svc.UpdateTeamChannel(c.Request.Context(), actor, c.Param("team"), c.Param("channel_id"), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamMemberProfilesList(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.ListTeamMemberProfiles(c.Request.Context(), actor, c.Param("team"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamMemberProfileSet(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	targetID, err := strconv.ParseInt(c.Param("member"), 10, 64)
	if err != nil || targetID < 1 {
		response.BadRequest(c, "无效的成员标识。")
		return
	}
	var in struct {
		JobTitle string `json:"job_title"`
	}
	if !strictCollabBody(c, 4<<10, &in) {
		return
	}
	out, err := svc.SetTeamMemberJobTitle(c.Request.Context(), actor, c.Param("team"), targetID, in.JobTitle)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

// --- Team Tasks & Review Flow ---

func (h *DoneBotHandler) CollabTeamTaskCounts(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.TeamTaskCountsForUser(c.Request.Context(), actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamTasksList(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.ListTeamTasks(c.Request.Context(), actor, c.Param("team"), donebotcollab.ListTeamTasksFilter{
		ChannelID: c.Query("channel_id"),
		Status:    c.Query("status"),
		Scope:     c.Query("scope"),
	})
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamTaskCreate(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in donebotcollab.CreateTeamTaskInput
	if !strictCollabBody(c, 64<<10, &in) {
		return
	}
	out, err := svc.CreateTeamTask(c.Request.Context(), actor, c.Param("team"), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamTaskDetail(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.GetTeamTaskDetail(c.Request.Context(), actor, c.Param("team"), c.Param("task_id"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamTaskUpdate(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in donebotcollab.UpdateTeamTaskInput
	if !strictCollabBody(c, 64<<10, &in) {
		return
	}
	out, err := svc.UpdateTeamTask(c.Request.Context(), actor, c.Param("team"), c.Param("task_id"), in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamTaskDelete(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, svc.DeleteTeamTask(c.Request.Context(), actor, c.Param("team"), c.Param("task_id"))) {
		response.Success(c, gin.H{"ok": true})
	}
}

func (h *DoneBotHandler) CollabTeamTaskCommentAdd(c *gin.Context) {
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
	out, err := svc.AddTeamTaskComment(c.Request.Context(), actor, c.Param("team"), c.Param("task_id"), in.Body)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamTaskSubmit(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		Body          string `json:"body"`
		NotifyChannel bool   `json:"notify_channel"`
	}
	if !strictCollabBody(c, 64<<10, &in) {
		return
	}
	out, err := svc.SubmitTeamTaskForReview(c.Request.Context(), actor, c.Param("team"), c.Param("task_id"), in.Body, in.NotifyChannel)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamTaskFileUpload(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	if c.Request.ContentLength > donebotcollab.MaxChatFileBytes {
		response.Error(c, http.StatusRequestEntityTooLarge, "上传的文件不能超过 100 MB。")
		return
	}
	kind := strings.TrimSpace(c.Query("kind"))
	if kind == "" {
		kind = "submission"
	}
	fileName := c.Query("name")
	caption := c.Query("body")
	notifyChannel := c.Query("notify_channel") == "1" || strings.EqualFold(c.Query("notify_channel"), "true")
	contentType := strings.TrimSpace(c.GetHeader("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	limitedBody := http.MaxBytesReader(c.Writer, c.Request.Body, donebotcollab.MaxChatFileBytes)
	out, err := svc.UploadTeamTaskFileUpdate(
		c.Request.Context(),
		actor,
		c.Param("team"),
		c.Param("task_id"),
		kind,
		fileName,
		contentType,
		caption,
		notifyChannel,
		limitedBody,
		c.Request.ContentLength,
	)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamTaskReview(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		Decision      string `json:"decision"`
		Body          string `json:"body"`
		NotifyChannel bool   `json:"notify_channel"`
	}
	if !strictCollabBody(c, 64<<10, &in) {
		return
	}
	out, err := svc.ReviewTeamTask(c.Request.Context(), actor, c.Param("team"), c.Param("task_id"), in.Decision, in.Body, in.NotifyChannel)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabTeamTaskFileDownload(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	f, meta, err := svc.OpenTeamTaskFile(c.Request.Context(), actor, c.Param("file_id"))
	if response.ErrorFrom(c, err) {
		return
	}
	defer func() { _ = f.Close() }()

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
