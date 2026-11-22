package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/donebotteam"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *DoneBotHandler) teamActor(c *gin.Context) (*donebotteam.Service, int64, bool) {
	if h.cloud == nil {
		response.Error(c, 503, "Team collaboration is unavailable")
		return nil, 0, false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Login required")
		return nil, 0, false
	}
	teams := h.cloud.Teams()
	if !teams.Enabled() {
		response.Error(c, 503, "Team collaboration is disabled")
		return nil, 0, false
	}
	c.Header("Cache-Control", "no-store")
	return teams, subject.UserID, true
}
func strictTeamBody(c *gin.Context, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "Invalid team request")
		return false
	}
	return true
}
func (h *DoneBotHandler) attachTeamAvatars(c *gin.Context, list []donebotteam.Team) {
	if h.collab == nil || len(list) == 0 {
		return
	}
	ids := make([]string, 0, len(list))
	for _, t := range list {
		ids = append(ids, t.ID)
	}
	if avatars, err := h.collab.LookupTeamAvatars(c.Request.Context(), ids); err == nil {
		for i := range list {
			if url, ok := avatars[list[i].ID]; ok {
				list[i].AvatarURL = url
			}
		}
	}
}

func (h *DoneBotHandler) Teams(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodPost {
		var in struct {
			Name string `json:"name"`
		}
		if !strictTeamBody(c, &in) {
			return
		}
		out, err := teams.Create(c.Request.Context(), actor, in.Name, c.GetHeader("Idempotency-Key"))
		if !response.ErrorFrom(c, err) {
			response.Success(c, out)
		}
		return
	}
	out, err := teams.List(c.Request.Context(), actor)
	if !response.ErrorFrom(c, err) {
		h.attachTeamAvatars(c, out)
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) Team(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodPatch {
		var in struct {
			Name string `json:"name"`
		}
		if !strictTeamBody(c, &in) {
			return
		}
		out, err := teams.Rename(c.Request.Context(), c.Param("team"), actor, in.Name)
		if !response.ErrorFrom(c, err) {
			response.Success(c, out)
		}
		return
	}
	out, err := teams.Access(c.Request.Context(), c.Param("team"), actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) TeamMembers(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	out, err := teams.Members(c.Request.Context(), c.Param("team"), actor)
	if response.ErrorFrom(c, err) {
		return
	}
	if h.collab != nil && len(out) > 0 {
		ids := make([]int64, 0, len(out))
		for _, member := range out {
			ids = append(ids, member.UserID)
		}
		if profiles, lookupErr := h.collab.LookupUserProfiles(c.Request.Context(), actor, ids); lookupErr == nil {
			for i := range out {
				if p, exists := profiles[out[i].UserID]; exists {
					out[i].Nickname = p.Nickname
					out[i].AvatarURL = p.AvatarURL
					out[i].Remark = p.Remark
				}
			}
		}
	}
	response.Success(c, out)
}
func (h *DoneBotHandler) TeamMember(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	target, err := strconv.ParseInt(c.Param("member"), 10, 64)
	if err != nil || target < 1 {
		response.BadRequest(c, "Invalid member")
		return
	}
	var in struct {
		Role donebotteam.Role `json:"role"`
	}
	remove := c.Request.Method == http.MethodDelete
	if !remove && !strictTeamBody(c, &in) {
		return
	}
	if !response.ErrorFrom(c, teams.ChangeMember(c.Request.Context(), c.Param("team"), actor, target, in.Role, remove)) {
		response.Success(c, gin.H{"ok": true})
	}
}
func (h *DoneBotHandler) TeamInvite(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodDelete {
		if !response.ErrorFrom(c, teams.RevokeInvitation(c.Request.Context(), c.Param("team"), actor, c.Param("invitation"))) {
			response.Success(c, gin.H{"ok": true})
		}
		return
	}
	var in struct {
		Email string           `json:"email"`
		Role  donebotteam.Role `json:"role"`
	}
	if !strictTeamBody(c, &in) {
		return
	}
	if in.Role == "" {
		in.Role = donebotteam.Member
	}
	out, err := teams.Invite(c.Request.Context(), c.Param("team"), actor, in.Email, in.Role)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) TeamAccept(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !strictTeamBody(c, &in) {
		return
	}
	out, err := teams.Accept(c.Request.Context(), actor, in.Token)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}
func (h *DoneBotHandler) TeamWorkspaces(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.cloud == nil || !h.cloud.Teams().Enabled() {
		response.Success(c, gin.H{"protocol": donebotteam.Protocol, "capabilities": []string{}, "workspaces": []any{}})
		return
	}
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	out, err := teams.List(c.Request.Context(), actor)
	if !response.ErrorFrom(c, err) {
		h.attachTeamAvatars(c, out)
		capabilities := []string{"identity", "local_execution", "shared_files"}
		if h.collab != nil && h.collab.Enabled() {
			capabilities = append(capabilities, "collaboration")
		}
		if cfg, cfgErr := h.cloud.BrowserConfig(); cfgErr == nil && cfg != nil {
			capabilities = append(capabilities, "cloud_access")
		}
		response.Success(c, gin.H{"protocol": donebotteam.Protocol, "capabilities": capabilities, "workspaces": out})
	}
}

func (h *DoneBotHandler) TeamInvitations(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	out, err := teams.TeamInvitations(c.Request.Context(), c.Param("team"), actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) TeamIncomingInvitations(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	out, err := teams.IncomingInvitations(c.Request.Context(), actor)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) TeamAcceptInvitation(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	out, err := teams.AcceptInvitation(c.Request.Context(), actor, c.Param("invitation"))
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *DoneBotHandler) TeamDeclineInvitation(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, teams.DeclineInvitation(c.Request.Context(), actor, c.Param("invitation"))) {
		response.Success(c, gin.H{"ok": true})
	}
}

func (h *DoneBotHandler) TeamLeave(c *gin.Context) {
	teams, actor, ok := h.teamActor(c)
	if !ok {
		return
	}
	if !response.ErrorFrom(c, teams.Leave(c.Request.Context(), c.Param("team"), actor)) {
		response.Success(c, gin.H{"ok": true})
	}
}
