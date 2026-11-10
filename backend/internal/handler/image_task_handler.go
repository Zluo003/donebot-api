package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type AsyncImageHandler struct {
	tasks     *service.ImageTaskService
	openAI    *OpenAIGatewayHandler
	estimator inflightReservationEstimator
	execute   func(platform string, c *gin.Context)
}

func NewAsyncImageHandler(tasks *service.ImageTaskService, openAI *OpenAIGatewayHandler) *AsyncImageHandler {
	h := &AsyncImageHandler{tasks: tasks, openAI: openAI}
	h.execute = h.executeWithGateway
	return h
}

// enabled reports whether the async image task feature is available. Object
// storage is the enablement gate: without it the endpoints are fully disabled
// so that large base64 results never land in Redis.
func (h *AsyncImageHandler) enabled() bool {
	return h != nil && h.tasks != nil && h.tasks.Enabled()
}

// pollable reports whether task lookups can be served. It is deliberately weaker
// than enabled(): results already written to Redis stay readable after the
// feature is switched off, so an in-flight task is never stranded.
func (h *AsyncImageHandler) pollable() bool {
	return h != nil && h.tasks != nil && h.tasks.Pollable()
}

// Submit accepts the same payload as the synchronous Images endpoint and
// returns before the upstream image generation begins.
func (h *AsyncImageHandler) Submit(c *gin.Context) {
	if !h.enabled() {
		imageTaskJSONError(c, http.StatusNotFound, "not_found_error", "async image tasks are not enabled")
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.UserID <= 0 || apiKey.ID <= 0 {
		imageTaskError(c, service.ErrImageTaskForbidden)
		return
	}
	platform := ""
	if apiKey.Group != nil {
		platform = apiKey.Group.Platform
	}
	compositeGroup := platform == service.PlatformComposite
	if compositeGroup {
		resolved, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
		if !ok {
			imageTaskJSONError(c, http.StatusBadRequest, "unsupported_platform", "composite image model could not be resolved to a supported platform")
			return
		}
		platform = resolved
	}
	if platform != service.PlatformOpenAI && platform != service.PlatformGrok {
		if compositeGroup || platform == service.PlatformGemini {
			imageTaskJSONError(c, http.StatusBadRequest, "unsupported_platform", "Images API is not supported for this platform")
			return
		}
		imageTaskJSONError(c, http.StatusNotFound, "not_found_error", "Images API is not supported for this platform")
		return
	}
	if !service.GroupAllowsImageGeneration(apiKey.Group) {
		imageTaskJSONError(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	if h == nil || h.tasks == nil || h.execute == nil {
		imageTaskError(c, service.ErrImageTaskUnavailable)
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			imageTaskJSONError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}
	if asyncImageRequestStreams(c.GetHeader("Content-Type"), body) {
		imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", "streaming image requests cannot be submitted as asynchronous tasks")
		return
	}
	if err := h.validateRequest(c, platform, body); err != nil {
		imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if !h.checkSecurityAuditBeforeSubmit(c, apiKey, platform, body) {
		return
	}
	if h.openAI != nil && platform == service.PlatformOpenAI && service.DoneBotMediaReceiptID(c.Request.Context()) != "" {
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		h.openAI.Images(c)
		return
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(append([]byte(strings.TrimPrefix(c.Request.URL.Path, "/v1")+"\n"+c.GetHeader("Content-Type")+"\n"), body...)))
	owner := service.ImageTaskOwner{UserID: apiKey.UserID, APIKeyID: apiKey.ID}
	task, found, err := h.tasks.LookupIdempotent(c.Request.Context(), owner, c.GetHeader("Idempotency-Key"), fingerprint)
	if err != nil {
		imageTaskError(c, err)
		return
	}
	created := false
	var reservation *service.InflightReservation
	transferred := false
	defer func() {
		if !transferred {
			reservation.HandlerDone()
		}
	}()
	if !found {
		var admitted bool
		reservation, admitted = h.reserveDoneBotBalanceBeforeSubmit(c, apiKey, platform, body)
		if !admitted {
			return
		}
		c.Request = c.Request.WithContext(service.WithInflightReservation(c.Request.Context(), reservation))
		task, created, err = h.tasks.CreateIdempotent(c.Request.Context(), owner, c.GetHeader("Idempotency-Key"), fingerprint)
		if err != nil {
			imageTaskError(c, err)
			return
		}
	}

	pollURL := imageTaskPollURL(c.Request.URL.Path, task.ID)
	c.Set(middleware2.DoneBotMediaProvider, "donebot")
	c.Set(middleware2.DoneBotMediaTaskID, task.ID)
	c.Header("Cache-Control", "no-store")
	c.Header("Location", pollURL)
	c.Header("Retry-After", "3")
	c.JSON(http.StatusAccepted, gin.H{
		"id":         task.ID,
		"task_id":    task.TaskID,
		"object":     task.Object,
		"status":     task.Status,
		"created_at": task.CreatedAt,
		"expires_at": task.ExpiresAt,
		"poll_url":   pollURL,
		"receipt_id": task.ReceiptID,
	})

	if created {
		taskCtx, recorder, cancel := newAsyncImageContext(c, body, h.tasks.ExecutionTimeout())
		go func() {
			defer reservation.HandlerDone()
			h.run(task.ID, platform, taskCtx, recorder, cancel)
		}()
		transferred = true
	}
}

func (h *AsyncImageHandler) checkSecurityAuditBeforeSubmit(c *gin.Context, apiKey *service.APIKey, platform string, body []byte) bool {
	if h == nil || h.openAI == nil {
		return true
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		imageTaskJSONError(c, http.StatusInternalServerError, "api_error", "User context not found")
		return false
	}
	model := ""
	moderationBody := body
	if platform == service.PlatformGrok {
		parsed := service.ParseGrokMediaRequest(c.GetHeader("Content-Type"), body)
		model, moderationBody = parsed.Model, parsed.ModerationBody()
	} else if h.openAI.gatewayService != nil {
		parsed, err := h.openAI.gatewayService.ParseOpenAIImagesRequest(c, body)
		if err != nil {
			imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return false
		}
		model, moderationBody = parsed.Model, parsed.ModerationBody()
	}
	if len(moderationBody) == 0 {
		c.Set(securityAuditCompletedContextKey, true)
		return true
	}
	reqLog := requestLogger(c, "handler.async_image.security_audit",
		zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", apiKey.ID), zap.String("model", model))
	decision := h.openAI.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, model, moderationBody)
	if decision != nil && !decision.AllowNextStage {
		h.openAI.openAISecurityAuditError(c, decision)
		return false
	}
	return true
}

func (h *AsyncImageHandler) Get(c *gin.Context) {
	// Polling deliberately does not require the feature to be enabled, only that
	// the task store is reachable. Turning the switch off in the admin UI must not
	// strand tasks that were already accepted — their results are still in Redis
	// and their submitters are still polling.
	if !h.pollable() {
		imageTaskJSONError(c, http.StatusNotFound, "not_found_error", "async image tasks are not enabled")
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.UserID <= 0 || apiKey.ID <= 0 {
		imageTaskError(c, service.ErrImageTaskForbidden)
		return
	}
	if h.openAI != nil && h.openAI.serveDoneBotDispatch(c, c.Param("task_id")) {
		return
	}
	task, err := h.tasks.Get(c.Request.Context(), service.ImageTaskOwner{UserID: apiKey.UserID, APIKeyID: apiKey.ID}, c.Param("task_id"))
	if err != nil {
		imageTaskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if task.Status == service.ImageTaskStatusProcessing {
		c.Header("Retry-After", "3")
	}
	c.JSON(http.StatusOK, task)
}

func (h *AsyncImageHandler) validateRequest(c *gin.Context, platform string, body []byte) error {
	if h.openAI == nil || h.openAI.gatewayService == nil {
		return nil
	}
	if platform == service.PlatformGrok {
		parsed := service.ParseGrokMediaRequest(c.GetHeader("Content-Type"), body)
		if strings.TrimSpace(parsed.Model) == "" {
			return errors.New("model is required")
		}
		return nil
	}
	parsed, err := h.openAI.gatewayService.ParseOpenAIImagesRequest(c, body)
	if err != nil {
		return err
	}
	if parsed.Stream {
		return errors.New("streaming image requests cannot be submitted as asynchronous tasks")
	}
	key, _ := middleware2.GetAPIKeyFromContext(c)
	if key != nil && strings.HasPrefix(key.Key, "sk-donebot-") {
		if err := parsed.ApplyDoneBotImagePolicy(parsed.Model); err != nil {
			return err
		}
	}
	return nil
}

func (h *AsyncImageHandler) executeWithGateway(platform string, c *gin.Context) {
	if h.openAI == nil {
		c.Set(middleware2.DoneBotMediaNotDispatched, true)
		imageTaskJSONError(c, http.StatusServiceUnavailable, "api_error", "image gateway is unavailable")
		return
	}
	if platform == service.PlatformGrok {
		h.openAI.GrokImages(c)
		return
	}
	h.openAI.Images(c)
}

func (h *AsyncImageHandler) run(taskID, platform string, taskCtx *gin.Context, recorder *httptest.ResponseRecorder, cancel context.CancelFunc) {
	defer cancel()
	pending := &imageTaskBilling{}
	base := context.WithValue(taskCtx.Request.Context(), imageTaskBillingContextKey{}, pending)
	taskCtx.Request = taskCtx.Request.WithContext(service.WithDoneBotMediaPrepaidSettlement(base))
	defer pending.finish(false)
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.L().Error("image_task.execution_panicked", zap.String("task_id", taskID), zap.Any("panic", recovered))
			h.failTask(taskCtx, taskID, http.StatusInternalServerError, imageTaskErrorPayload("api_error", "image generation task panicked"))
		}
	}()

	taskCtx.Set(middleware2.DoneBotMediaNotDispatched, false)
	h.execute(platform, taskCtx)
	body := bytes.TrimSpace(recorder.Body.Bytes())
	if pending, _ := taskCtx.Get(doneBotImageSettlementPending); pending == true {
		h.failTask(taskCtx, taskID, http.StatusBadGateway, imageTaskErrorPayload("settlement_pending", "image settlement needs reconciliation; do not resubmit"))
		return
	}
	if err := taskCtx.Request.Context().Err(); err != nil && len(body) == 0 {
		h.failTask(taskCtx, taskID, http.StatusGatewayTimeout, imageTaskErrorPayload("timeout_error", "image generation task timed out"))
		return
	}
	statusCode := recorder.Code
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		if len(body) == 0 || !json.Valid(body) {
			h.failTask(taskCtx, taskID, http.StatusBadGateway, imageTaskErrorPayload("api_error", "upstream returned an invalid image response"))
			return
		}
		delivered, err := h.tasks.CompleteDelivered(taskCtx.Request.Context(), taskID, statusCode, json.RawMessage(body))
		if err != nil {
			logger.L().Error("image_task.complete_store_failed", zap.String("task_id", taskID), zap.Error(err))
			h.failTask(taskCtx, taskID, http.StatusBadGateway, imageTaskErrorPayload("api_error", "failed to persist generated image result"))
			return
		}
		if delivered {
			pending.finish(true)
		}
		return
	}
	if h.openAI != nil {
		h.openAI.refundDoneBotRejectedMedia(taskCtx, statusCode)
	}
	taskError := extractImageTaskError(body)
	if service.DoneBotMediaReceiptID(taskCtx.Request.Context()) != "" {
		if diagnostic := service.DoneBotMediaErrorDiagnostic(statusCode, recorder.Header().Get("X-Request-Id"), body); diagnostic != nil {
			taskError, _ = json.Marshal(diagnostic)
		}
	}
	if notDispatched, _ := taskCtx.Get(middleware2.DoneBotMediaNotDispatched); notDispatched == true && statusCode >= http.StatusInternalServerError {
		logAsyncImageTaskFailure(taskCtx, taskID, statusCode, taskError)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.tasks.FailNotDispatched(ctx, taskID, statusCode, taskError); err != nil {
			logger.L().Error("image_task.failure_store_failed", zap.String("task_id", taskID), zap.Error(err))
		}
		return
	}
	h.failTask(taskCtx, taskID, statusCode, taskError)
}

func (h *AsyncImageHandler) failTask(taskCtx *gin.Context, taskID string, statusCode int, taskErr json.RawMessage) {
	logAsyncImageTaskFailure(taskCtx, taskID, statusCode, taskErr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.tasks.Fail(ctx, taskID, statusCode, taskErr); err != nil {
		logger.L().Error("image_task.failure_store_failed", zap.String("task_id", taskID), zap.Error(err))
	}
}

func logAsyncImageTaskFailure(c *gin.Context, taskID string, statusCode int, taskErr json.RawMessage) {
	fields := []zap.Field{
		zap.String("task_id", taskID),
		zap.Int("status_code", statusCode),
		zap.String("model", c.GetString(opsModelKey)),
		zap.String("upstream_model", c.GetString(opsUpstreamModelKey)),
		zap.Int64("account_id", c.GetInt64(opsAccountIDKey)),
		zap.Bool("marked_not_dispatched", c.GetBool(middleware2.DoneBotMediaNotDispatched)),
	}
	if diagnostic := service.DoneBotMediaErrorDiagnostic(statusCode, c.Writer.Header().Get("X-Request-Id"), taskErr); diagnostic != nil {
		fields = append(fields,
			zap.String("error_type", diagnostic.Type),
			zap.String("error_code", diagnostic.Code),
			zap.String("upstream_request_id", diagnostic.RequestID),
			zap.String("error_message", diagnostic.Message),
		)
	}
	requestLogger(c, "handler.async_image").Warn("image_task.execution_failed", fields...)
}

func newAsyncImageContext(c *gin.Context, body []byte, timeoutDuration time.Duration) (*gin.Context, *httptest.ResponseRecorder, context.CancelFunc) {
	base := context.WithoutCancel(c.Request.Context())
	executionCtx, cancel := context.WithTimeout(base, timeoutDuration)
	request := c.Request.Clone(executionCtx)
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	request.ContentLength = int64(len(body))
	request.URL.Path = strings.TrimSuffix(request.URL.Path, "/async")

	taskCtx := c.Copy()
	recorder := httptest.NewRecorder()
	recorderCtx, _ := gin.CreateTestContext(recorder)
	taskCtx.Writer = recorderCtx.Writer
	taskCtx.Request = request
	return taskCtx, recorder, cancel
}

func asyncImageRequestStreams(contentType string, body []byte) bool {
	if isMultipartImagesContentType(contentType) {
		return false
	}
	var envelope struct {
		Stream bool `json:"stream"`
	}
	return json.Unmarshal(body, &envelope) == nil && envelope.Stream
}

func imageTaskPollURL(submitPath, taskID string) string {
	if strings.HasPrefix(submitPath, "/v1/") {
		return "/v1/images/tasks/" + taskID
	}
	return "/images/tasks/" + taskID
}

func extractImageTaskError(body []byte) json.RawMessage {
	if json.Valid(body) {
		var envelope struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil && len(envelope.Error) > 0 && json.Valid(envelope.Error) {
			return envelope.Error
		}
		return json.RawMessage(body)
	}
	return imageTaskErrorPayload("api_error", "image generation failed")
}

func imageTaskErrorPayload(errorType, message string) json.RawMessage {
	data, _ := json.Marshal(gin.H{"type": errorType, "message": message})
	return data
}

func imageTaskError(c *gin.Context, err error) {
	status := infraerrors.Code(err)
	code := infraerrors.Reason(err)
	message := infraerrors.Message(err)
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	if strings.TrimSpace(code) == "" {
		code = "IMAGE_TASK_ERROR"
	}
	imageTaskJSONError(c, status, code, message)
}

func imageTaskJSONError(c *gin.Context, status int, code, message string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"error": gin.H{"type": code, "code": code, "message": message}})
}
