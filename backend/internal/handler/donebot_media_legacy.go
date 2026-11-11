package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) DoneBotLegacyMediaJob(c *gin.Context) {
	key, _, ok := h.doneBotIntentIdentity(c)
	if !ok {
		return
	}
	if h.gatewayService == nil || key == nil || !strings.HasPrefix(key.Key, "sk-donebot-") {
		response.NotFound(c, "Media job not found")
		return
	}
	task, err := h.gatewayService.LookupDoneBotDispatch(c.Request.Context(), key.UserID, key.ID, c.Param("job_id"))
	if errors.Is(err, service.ErrDoneBotMediaNotFound) || task != nil && !task.DoneBotIntent {
		response.NotFound(c, "Media job not found")
		return
	}
	if response.ErrorFrom(c, err) {
		return
	}
	h.writeDoneBotIntentTask(c, task, http.StatusOK)
}

func (h *OpenAIGatewayHandler) DoneBotLegacyMediaByIdempotency(c *gin.Context) {
	key, owner, ok := h.doneBotIntentIdentity(c)
	if !ok {
		return
	}
	if h.doneBot.media == nil || h.gatewayService == nil || key == nil || !strings.HasPrefix(key.Key, "sk-donebot-") {
		response.NotFound(c, "Media job not found")
		return
	}
	op, err := h.doneBot.media.LookupByIdempotencyKey(c.Request.Context(), owner, c.Param("key"))
	if errors.Is(err, service.ErrDoneBotMediaNotFound) {
		response.NotFound(c, "Media job not found")
		return
	}
	if response.ErrorFrom(c, err) {
		return
	}
	h.serveDoneBotIntentOperation(c, key, op)
}

func (h *OpenAIGatewayHandler) doneBotIntentIdentity(c *gin.Context) (*service.APIKey, service.DoneBotMediaOwner, bool) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.UserID <= 0 || key.ID <= 0 || key.GroupID == nil || *key.GroupID <= 0 || !strings.HasPrefix(key.Key, "sk-donebot-") {
		response.Unauthorized(c, "API key required")
		return nil, service.DoneBotMediaOwner{}, false
	}
	if h.doneBot == nil || h.doneBot.media == nil {
		response.ErrorFrom(c, service.ErrDoneBotGroupUnavailable)
		return nil, service.DoneBotMediaOwner{}, false
	}
	owner, err := h.doneBot.media.ResolveOwner(c.Request.Context(), key.UserID, key.ID, *key.GroupID)
	if response.ErrorFrom(c, err) {
		return nil, service.DoneBotMediaOwner{}, false
	}
	return key, owner, true
}

func (h *OpenAIGatewayHandler) serveDoneBotIntentOperation(c *gin.Context, key *service.APIKey, op *service.DoneBotMediaOperation) {
	if op == nil || op.TaskID == "" || key == nil {
		response.NotFound(c, "Media job not found")
		return
	}
	task, err := h.gatewayService.LookupDoneBotDispatch(c.Request.Context(), key.UserID, key.ID, op.TaskID)
	if err == nil && task != nil {
		h.writeDoneBotIntentTask(c, task, http.StatusAccepted)
		return
	}
	if !errors.Is(err, service.ErrDoneBotMediaNotFound) {
		response.ErrorFrom(c, err)
		return
	}
	status := http.StatusAccepted
	if op.Status != "dispatching" {
		status = http.StatusOK
	}
	kind := op.Kind
	if kind == "music" {
		kind = "audio"
	}
	h.writeDoneBotIntentOperation(c, op, kind, op.Operation, status)
}

func (h *OpenAIGatewayHandler) writeDoneBotIntentTask(c *gin.Context, task *service.DoneBotDispatch, status int) {
	if task == nil || !task.DoneBotIntent {
		response.NotFound(c, "Media job not found")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-DoneBot-Receipt-ID", task.ReceiptID)
	if task.State != "completed" && task.State != "failed" {
		c.Header("Retry-After", "3")
	}
	c.JSON(status, task.IntentJob(task.IntentKind, task.IntentOperation))
}

func (h *OpenAIGatewayHandler) writeDoneBotIntentOperation(c *gin.Context, op *service.DoneBotMediaOperation, kind, operation string, status int) {
	state := "queued"
	switch op.Status {
	case "failed", "refunded", "cancelled":
		state = "failed"
	case "uncertain":
		state = "uncertain"
	}
	job := service.DoneBotMediaIntentJob{
		JobID: op.TaskID, TaskID: op.TaskID, ReceiptID: op.ReceiptID, Kind: kind, Operation: operation, Status: state,
		RetryEligibility: service.DoneBotMediaRetryEligibility{Eligible: false, State: "blocked", Reason: "processing"},
		Artifacts:        []service.DoneBotMediaIntentArtifact{},
	}
	if state == "failed" {
		job.RetryEligibility.Reason = "request_rejected"
		job.Diagnostic = service.DoneBotMediaErrorDiagnostic(op.ReplayStatus, "", op.ReplayBody)
	}
	if state == "uncertain" {
		job.RetryEligibility.Reason = "dispatch_unconfirmed"
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-DoneBot-Receipt-ID", op.ReceiptID)
	if state == "queued" {
		c.Header("Retry-After", "3")
	}
	c.JSON(status, job)
}
