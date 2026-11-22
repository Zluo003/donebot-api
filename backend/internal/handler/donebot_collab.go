package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

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
	if !strictCollabBody(c, 2<<20, &in) {
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
