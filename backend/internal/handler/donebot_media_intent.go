package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) DoneBotMediaIntent(c *gin.Context) {
	key, owner, ok := h.doneBotIntentIdentity(c)
	if !ok {
		return
	}
	if h.doneBot.media == nil || h.gatewayService == nil || h.doneBot.catalog == nil {
		response.ErrorFrom(c, service.ErrDoneBotGroupUnavailable)
		return
	}
	c.Header("Cache-Control", "no-store")
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, service.DoneBotMediaIntentBodyLimit+1))
	if err != nil || len(body) == 0 || len(body) > service.DoneBotMediaIntentBodyLimit {
		response.ErrorFrom(c, service.ErrDoneBotMediaInvalid)
		return
	}
	contentType := c.GetHeader("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	fingerprint := service.DoneBotMediaFingerprint(http.MethodPost, c.Request.URL.Path, contentType, body)
	op, lookupErr := h.doneBot.media.LookupByIdempotencyKey(c.Request.Context(), owner, c.GetHeader("Idempotency-Key"))
	if lookupErr == nil {
		if op.Fingerprint != fingerprint {
			response.ErrorFrom(c, service.ErrDoneBotMediaConflict)
			return
		}
		h.serveDoneBotIntentOperation(c, key, op)
		return
	}
	if !errors.Is(lookupErr, service.ErrDoneBotMediaNotFound) {
		response.ErrorFrom(c, lookupErr)
		return
	}
	request, err := service.DecodeDoneBotMediaIntentRequest(body)
	if response.ErrorFrom(c, err) {
		return
	}
	planner, ok := h.doneBot.catalog.(interface {
		PlanMediaIntent(context.Context, service.DoneBotMediaOwner, service.DoneBotMediaIntentRequest, service.DoneBotMediaIntentAssetReader) (*service.DoneBotMediaIntentPlan, error)
	})
	if !ok {
		response.ErrorFrom(c, service.ErrDoneBotGroupUnavailable)
		return
	}
	plan, err := planner.PlanMediaIntent(c.Request.Context(), owner, request, h.assets)
	if response.ErrorFrom(c, err) {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID != key.UserID {
		response.ErrorFrom(c, service.ErrDoneBotMediaInvalid)
		return
	}
	moderationBody, err := service.DoneBotMediaIntentModerationBody(c.Request.Context(), request, h.assets, owner.APIKeyID)
	if response.ErrorFrom(c, err) {
		return
	}
	if decision := h.checkSecurityAudit(c, requestLogger(c, "handler.openai_gateway.donebot_media_intent"), key, subject,
		service.ContentModerationProtocolOpenAIImages, plan.ModelID, moderationBody); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}
	operationKind := request.Intent.Kind
	if operationKind == "audio" {
		operationKind = "music"
	}
	begin, err := h.doneBot.media.Begin(c.Request.Context(), service.DoneBotMediaBeginInput{
		Owner: owner, IdempotencyKey: c.GetHeader("Idempotency-Key"), Fingerprint: fingerprint,
		Kind: operationKind, Operation: request.Intent.Operation, Model: plan.ModelID,
		Method: http.MethodPost, Path: c.Request.URL.Path, EstimatedCents: plan.EstimatedCents,
	})
	if begin != nil && begin.Operation != nil {
		c.Header("X-DoneBot-Receipt-ID", begin.Operation.ReceiptID)
	}
	if err != nil {
		if begin != nil && begin.Operation != nil && errors.Is(err, service.ErrDoneBotMediaPending) {
			h.serveDoneBotIntentOperation(c, key, begin.Operation)
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	if begin.Replay {
		h.serveDoneBotIntentOperation(c, key, begin.Operation)
		return
	}
	ctx := service.WithDoneBotMediaReceipt(c.Request.Context(), begin.Operation.ReceiptID)
	task, err := h.gatewayService.SubmitDoneBotDispatch(ctx, key, nil, service.DoneBotDispatch{
		Candidates: plan.DispatchCandidates, DoneBotIntent: true, IntentKind: request.Intent.Kind, IntentOperation: request.Intent.Operation,
	})
	if err != nil {
		stored, lookupErr := h.gatewayService.LookupDoneBotDispatch(c.Request.Context(), key.UserID, key.ID, begin.Operation.TaskID)
		if lookupErr == nil && stored != nil {
			h.writeDoneBotIntentTask(c, stored, http.StatusAccepted)
			return
		}
		if !errors.Is(lookupErr, service.ErrDoneBotMediaNotFound) {
			h.writeDoneBotIntentUnconfirmed(c, begin.Operation, request.Intent.Kind, request.Intent.Operation)
			return
		}
		status := infraerrors.Code(err)
		if status < 400 || status > 599 {
			status = http.StatusServiceUnavailable
		}
		failure := doneBotIntentFailureBody(infraerrors.Reason(err))
		completion := service.DoneBotMediaCompletion{
			Status: "failed", TaskID: begin.Operation.TaskID, TaskKey: begin.Operation.TaskKey, Provider: begin.Operation.Provider,
			UsageRequestID: begin.Operation.UsageRequestID, HTTPStatus: status, ContentType: "application/json", Body: failure,
			Diagnostic: service.DoneBotMediaErrorDiagnostic(status, "", failure),
		}
		if _, completeErr := h.doneBot.media.Complete(c.Request.Context(), owner, begin.Operation.ReceiptID, completion); completeErr != nil {
			c.Header("X-DoneBot-Receipt-ID", begin.Operation.ReceiptID)
			h.writeDoneBotIntentOperation(c, begin.Operation, request.Intent.Kind, request.Intent.Operation, http.StatusAccepted)
			return
		}
		c.Writer.Header().Del("X-DoneBot-Receipt-ID")
		c.Data(status, "application/json", failure)
		return
	}
	h.writeDoneBotIntentTask(c, task, http.StatusAccepted)
}

func (h *OpenAIGatewayHandler) DoneBotMediaIntentJob(c *gin.Context) {
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

func (h *OpenAIGatewayHandler) DoneBotMediaIntentByIdempotency(c *gin.Context) {
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
	h.writeDoneBotIntentOperation(c, op, opKind(op.Kind), op.Operation, status)
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

func (h *OpenAIGatewayHandler) writeDoneBotIntentUnconfirmed(c *gin.Context, op *service.DoneBotMediaOperation, kind, operation string) {
	job := service.DoneBotMediaIntentJob{
		JobID: op.TaskID, TaskID: op.TaskID, ReceiptID: op.ReceiptID, Kind: kind, Operation: operation,
		Status: "uncertain", RetryEligibility: service.DoneBotMediaRetryEligibility{Eligible: false, State: "blocked", Reason: "dispatch_unconfirmed"},
		Artifacts: []service.DoneBotMediaIntentArtifact{},
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-DoneBot-Receipt-ID", op.ReceiptID)
	c.Header("Retry-After", "3")
	c.JSON(http.StatusAccepted, job)
}

func doneBotIntentFailureBody(reason string) []byte {
	if reason == "" || len(reason) > 96 {
		reason = "media_task_not_accepted"
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]string{
		"type": "api_error", "code": reason, "message": "The media task could not be accepted.",
	}})
	return body
}

func opKind(kind string) string {
	if kind == "music" {
		return "audio"
	}
	return kind
}
