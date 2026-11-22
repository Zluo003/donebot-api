package handler

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/donebotcollab"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) collabActor(c *gin.Context) (*donebotcollab.Service, int64, bool) {
	if h.collab == nil || !h.collab.Enabled() {
		response.Error(c, http.StatusServiceUnavailable, "Remote collaboration is unavailable")
		return nil, 0, false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Login required")
		return nil, 0, false
	}
	c.Header("Cache-Control", "no-store")
	return h.collab, subject.UserID, true
}

func strictCollabBody(c *gin.Context, maxBytes int64, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "Invalid collaboration request")
		return false
	}
	return true
}

// --- Friends ---

func (h *DoneBotHandler) CollabFriends(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.ListFriends(c.Request.Context(), actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabFriendRequestCreate(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in struct {
		Email string `json:"email"`
		Note  string `json:"note"`
	}
	if !strictCollabBody(c, 16<<10, &in) {
		return
	}
	out, err := svc.SendFriendRequest(c.Request.Context(), actor, in.Email, in.Note)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabFriendRequestAccept(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.AcceptFriendRequest(c.Request.Context(), actor, c.Param("id"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabFriendRequestReject(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, svc.RejectFriendRequest(c.Request.Context(), actor, c.Param("id"))) {
		response.Success(c, gin.H{"ok": true})
	}
}

func (h *DoneBotHandler) CollabFriendRemove(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	target, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || target < 1 {
		response.BadRequest(c, "Invalid friend user id")
		return
	}
	if !response.ErrorFrom(c, svc.RemoveFriend(c.Request.Context(), actor, target)) {
		response.Success(c, gin.H{"ok": true})
	}
}

func (h *DoneBotHandler) CollabFriendRemarkSet(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	target, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || target < 1 {
		response.BadRequest(c, "Invalid friend user id")
		return
	}
	var in struct {
		Remark string `json:"remark"`
	}
	if !strictCollabBody(c, 4<<10, &in) {
		return
	}
	remark, err := svc.SetFriendRemark(c.Request.Context(), actor, target, in.Remark)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"user_id": target, "remark": remark})
	}
}

// --- Shared Folders ---

func (h *DoneBotHandler) CollabFolders(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.ListFolders(c.Request.Context(), actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabFolderSave(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in donebotcollab.SaveFolderInput
	if !strictCollabBody(c, 128<<10, &in) {
		return
	}
	if id := c.Param("id"); id != "" {
		in.ID = id
	}
	out, err := svc.SaveFolder(c.Request.Context(), actor, in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabFolderDelete(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, svc.DeleteFolder(c.Request.Context(), actor, c.Param("id"))) {
		response.Success(c, gin.H{"ok": true})
	}
}

type collabFolderInvokeRequest struct {
	Action         string `json:"action"`
	Path           string `json:"path,omitempty"`
	Content        string `json:"content,omitempty"`
	Encoding       string `json:"encoding,omitempty"`
	OldText        string `json:"old_text,omitempty"`
	NewText        string `json:"new_text,omitempty"`
	ExpectedSha256 string `json:"expected_sha256,omitempty"`
}

func (h *DoneBotHandler) CollabFolderInvoke(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in collabFolderInvokeRequest
	if !strictCollabBody(c, 1<<20, &in) {
		return
	}
	authz, err := svc.AuthorizeInvoke(c.Request.Context(), actor, c.Param("id"), in.Action)
	if response.ErrorFrom(c, err) {
		return
	}
	rawOp, err := json.Marshal(in)
	if err != nil {
		response.BadRequest(c, "Invalid operation payload")
		return
	}
	result, err := svc.Relay().Invoke(c.Request.Context(), authz.OwnerUserID, authz.InstallationID, donebotcollab.RelayJob{
		FolderID:      authz.FolderID,
		CallerUserID:  authz.CallerUserID,
		CallerEmail:   authz.CallerEmail,
		GrantedAccess: authz.GrantedAccess,
		Operation:     rawOp,
	})
	if response.ErrorFrom(c, err) {
		return
	}
	var decoded any
	if err := json.Unmarshal(result, &decoded); err != nil {
		response.Error(c, http.StatusBadGateway, "Invalid response from shared folder host")
		return
	}
	response.Success(c, gin.H{"result": decoded})
}

// --- Runtime Relay (Installation-Authenticated) ---

func (h *DoneBotHandler) CollabRelayPoll(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	installationID := c.GetHeader("X-DoneBot-Installation")
	if installationID == "" {
		response.Unauthorized(c, "Installation identity required")
		return
	}
	job, err := svc.Relay().Poll(c.Request.Context(), actor, installationID)
	if err != nil {
		if c.Request.Context().Err() != nil {
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"job": job})
}

func (h *DoneBotHandler) CollabRelayResult(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	installationID := c.GetHeader("X-DoneBot-Installation")
	if installationID == "" {
		response.Unauthorized(c, "Installation identity required")
		return
	}
	var in struct {
		JobID  string          `json:"job_id"`
		Result json.RawMessage `json:"result"`
	}
	if !strictCollabBody(c, 4<<20, &in) {
		return
	}
	if in.JobID == "" || len(in.Result) == 0 || !json.Valid(in.Result) {
		response.BadRequest(c, "Invalid relay result payload")
		return
	}
	if !response.ErrorFrom(c, svc.Relay().Complete(actor, installationID, in.JobID, in.Result)) {
		response.Success(c, gin.H{"ok": true})
	}
}

// --- 100 MiB Streaming File Transfer Relay (zero disk persistence on gateway) ---

func cleanTransferRelPath(raw string) (string, bool) {
	clean := strings.TrimSpace(raw)
	if clean == "" || len(clean) > 2048 || strings.ContainsAny(clean, "\\:\x00") || strings.HasPrefix(clean, "/") {
		return "", false
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", false
		}
	}
	return clean, true
}

func (h *DoneBotHandler) CollabFolderFileDownload(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	relPath, valid := cleanTransferRelPath(c.Query("path"))
	if !valid {
		response.BadRequest(c, "请输入有效的相对文件路径。")
		return
	}
	authz, err := svc.AuthorizeInvoke(c.Request.Context(), actor, c.Param("id"), "read_file")
	if response.ErrorFrom(c, err) {
		return
	}
	meta, reader, release, err := svc.Relay().StartDownload(
		c.Request.Context(),
		authz.OwnerUserID,
		authz.InstallationID,
		authz.FolderID,
		authz.CallerUserID,
		authz.CallerEmail,
		authz.GrantedAccess,
		relPath,
	)
	if response.ErrorFrom(c, err) {
		return
	}

	contentType := strings.TrimSpace(meta.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	fileName := path.Base(relPath)
	if strings.TrimSpace(meta.FileName) != "" {
		fileName = path.Base(strings.TrimSpace(meta.FileName))
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fileName}))
	c.Header("X-Content-Type-Options", "nosniff")
	if meta.Size >= 0 && meta.Size <= donebotcollab.MaxTransferBytes {
		c.Header("Content-Length", strconv.FormatInt(meta.Size, 10))
	}
	if meta.Sha256 != "" {
		c.Header("X-File-Sha256", meta.Sha256)
	}
	c.Status(http.StatusOK)
	_, copyErr := io.Copy(c.Writer, io.LimitReader(reader, donebotcollab.MaxTransferBytes))
	release(copyErr)
}

func (h *DoneBotHandler) CollabFolderFileUpload(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	relPath, valid := cleanTransferRelPath(c.Query("path"))
	if !valid {
		response.BadRequest(c, "请输入有效的相对文件路径。")
		return
	}
	if c.Request.ContentLength > donebotcollab.MaxTransferBytes {
		response.Error(c, http.StatusRequestEntityTooLarge, "上传文件不能超过 100 MB。")
		return
	}
	authz, err := svc.AuthorizeInvoke(c.Request.Context(), actor, c.Param("id"), "write_file")
	if response.ErrorFrom(c, err) {
		return
	}
	limitedBody := http.MaxBytesReader(c.Writer, c.Request.Body, donebotcollab.MaxTransferBytes)
	size := c.Request.ContentLength
	contentType := strings.TrimSpace(c.GetHeader("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	result, err := svc.Relay().RelayUpload(
		c.Request.Context(),
		authz.OwnerUserID,
		authz.InstallationID,
		authz.FolderID,
		authz.CallerUserID,
		authz.CallerEmail,
		authz.GrantedAccess,
		relPath,
		size,
		contentType,
		limitedBody,
	)
	if response.ErrorFrom(c, err) {
		return
	}
	var decoded any
	if err := json.Unmarshal(result, &decoded); err != nil {
		response.Error(c, http.StatusBadGateway, "Invalid response from shared folder host")
		return
	}
	response.Success(c, gin.H{"result": decoded})
}

func (h *DoneBotHandler) CollabRelayTransferUpload(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	installationID := c.GetHeader("X-DoneBot-Installation")
	if installationID == "" {
		response.Unauthorized(c, "Installation identity required")
		return
	}
	if c.Request.ContentLength > donebotcollab.MaxTransferBytes {
		response.Error(c, http.StatusRequestEntityTooLarge, "共享文件不能超过 100 MB。")
		return
	}
	size := c.Request.ContentLength
	if raw := strings.TrimSpace(c.GetHeader("X-File-Size")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed >= 0 {
			size = parsed
		}
	}
	if size > donebotcollab.MaxTransferBytes {
		response.Error(c, http.StatusRequestEntityTooLarge, "共享文件不能超过 100 MB。")
		return
	}
	meta := donebotcollab.DownloadMeta{
		FileName:    strings.TrimSpace(c.GetHeader("X-File-Name")),
		ContentType: strings.TrimSpace(c.GetHeader("X-File-Content-Type")),
		Size:        size,
		Sha256:      strings.TrimSpace(c.GetHeader("X-File-Sha256")),
	}
	limitedBody := http.MaxBytesReader(c.Writer, c.Request.Body, donebotcollab.MaxTransferBytes)
	if !response.ErrorFrom(c, svc.Relay().ProvideDownload(c.Request.Context(), actor, installationID, c.Param("transfer_id"), meta, limitedBody)) {
		response.Success(c, gin.H{"ok": true})
	}
}

func (h *DoneBotHandler) CollabRelayTransferDownload(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	installationID := c.GetHeader("X-DoneBot-Installation")
	if installationID == "" {
		response.Unauthorized(c, "Installation identity required")
		return
	}
	reader, size, contentType, finish, err := svc.Relay().ClaimUpload(actor, installationID, c.Param("transfer_id"))
	if response.ErrorFrom(c, err) {
		return
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	if size >= 0 && size <= donebotcollab.MaxTransferBytes {
		c.Header("Content-Length", strconv.FormatInt(size, 10))
	}
	c.Status(http.StatusOK)
	_, copyErr := io.Copy(c.Writer, io.LimitReader(reader, donebotcollab.MaxTransferBytes+1))
	finish(copyErr)
}

func (h *DoneBotHandler) CollabRelayTransferFail(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	installationID := c.GetHeader("X-DoneBot-Installation")
	if installationID == "" {
		response.Unauthorized(c, "Installation identity required")
		return
	}
	var in struct {
		Error string `json:"error"`
	}
	if !strictCollabBody(c, 16<<10, &in) {
		return
	}
	if !response.ErrorFrom(c, svc.Relay().FailTransfer(actor, installationID, c.Param("transfer_id"), in.Error)) {
		response.Success(c, gin.H{"ok": true})
	}
}

// --- Shared Bots (Templates without Memory) ---

func (h *DoneBotHandler) CollabBots(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.ListBots(c.Request.Context(), actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabBotSave(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	var in donebotcollab.SaveBotInput
	if !strictCollabBody(c, donebotcollab.MaxBotPackageSize+(128<<10), &in) {
		return
	}
	if id := c.Param("id"); id != "" {
		in.ID = id
	}
	out, err := svc.SaveBot(c.Request.Context(), actor, in)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabBotPackage(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	out, err := svc.GetBotPackage(c.Request.Context(), actor, c.Param("id"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) CollabBotDelete(c *gin.Context) {
	svc, actor, ok := h.collabActor(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, svc.DeleteBot(c.Request.Context(), actor, c.Param("id"))) {
		response.Success(c, gin.H{"ok": true})
	}
}
