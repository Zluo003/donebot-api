package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	ImageTaskStatusProcessing = "processing"
	ImageTaskStatusCompleted  = "completed"
	ImageTaskStatusFailed     = "failed"
	ImageTaskStatusUncertain  = "uncertain"

	defaultImageTaskTTL              = 24 * time.Hour
	defaultImageTaskExecutionTimeout = 30 * time.Minute
)

var (
	ErrImageTaskNotFound    = infraerrors.New(http.StatusNotFound, "IMAGE_TASK_NOT_FOUND", "image task not found")
	ErrImageTaskForbidden   = infraerrors.New(http.StatusForbidden, "IMAGE_TASK_FORBIDDEN", "image task does not belong to this API key")
	ErrImageTaskUnavailable = infraerrors.New(http.StatusServiceUnavailable, "IMAGE_TASK_UNAVAILABLE", "image task storage is unavailable")
)

// ImageTaskRecord is the private Redis representation of an asynchronous image
// request. Ownership fields are intentionally omitted from the public view.
type ImageTaskRecord struct {
	ReceiptID            string          `json:"receipt_id,omitempty"`
	ExecutionDeadlineAt  int64           `json:"execution_deadline_at,omitempty"`
	IdempotencyExpiresAt int64           `json:"idempotency_expires_at,omitempty"`
	ID                   string          `json:"id"`
	UserID               int64           `json:"user_id"`
	APIKeyID             int64           `json:"api_key_id"`
	Status               string          `json:"status"`
	HTTPStatus           int             `json:"http_status,omitempty"`
	Result               json.RawMessage `json:"result,omitempty"`
	Error                json.RawMessage `json:"error,omitempty"`
	CreatedAt            int64           `json:"created_at"`
	CompletedAt          *int64          `json:"completed_at,omitempty"`
	ExpiresAt            int64           `json:"expires_at"`
}

// ImageTask is the API-safe task representation returned to callers.
type ImageTask struct {
	ReceiptID   string          `json:"receipt_id,omitempty"`
	ID          string          `json:"id"`
	TaskID      string          `json:"task_id"`
	Object      string          `json:"object"`
	Status      string          `json:"status"`
	HTTPStatus  int             `json:"http_status,omitempty"`
	ImageURL    string          `json:"image_url,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       json.RawMessage `json:"error,omitempty"`
	CreatedAt   int64           `json:"created_at"`
	CompletedAt *int64          `json:"completed_at,omitempty"`
	ExpiresAt   int64           `json:"expires_at"`
}

type ImageTaskOwner struct {
	UserID   int64
	APIKeyID int64
}

type ImageTaskStore interface {
	Save(ctx context.Context, task *ImageTaskRecord, ttl time.Duration) error
	Get(ctx context.Context, id string) (*ImageTaskRecord, error)
}

// ImageStorageResolver reports the currently effective object-storage binding.
// It exists so the async image feature can be switched on and off from the admin
// UI without a restart: the wiring below is fixed at startup, but the answer to
// "is object storage configured right now" is re-read (and cached) per call.
type ImageStorageResolver func() (uploader *ImageResultUploader, enabled bool)

type ImageTaskService struct {
	store            ImageTaskStore
	uploader         *ImageResultUploader
	enabled          bool
	resolve          ImageStorageResolver
	ttl              time.Duration
	executionTimeout time.Duration
}

func NewImageTaskService(store ImageTaskStore) *ImageTaskService {
	return NewImageTaskServiceWithOptions(store, defaultImageTaskTTL, defaultImageTaskExecutionTimeout)
}

func NewImageTaskServiceWithOptions(store ImageTaskStore, ttl, executionTimeout time.Duration) *ImageTaskService {
	if ttl <= 0 {
		ttl = defaultImageTaskTTL
	}
	if executionTimeout <= 0 {
		executionTimeout = defaultImageTaskExecutionTimeout
	}
	return &ImageTaskService{store: store, ttl: ttl, executionTimeout: executionTimeout}
}

// NewImageTaskServiceWithUploader 构造一个已启用的图片任务服务：结果会先经 uploader
// 转存到对象存储再落 Redis。uploader 为 nil 时不做转存（仅用于测试）。
func NewImageTaskServiceWithUploader(store ImageTaskStore, uploader *ImageResultUploader, ttl, executionTimeout time.Duration) *ImageTaskService {
	s := NewImageTaskServiceWithOptions(store, ttl, executionTimeout)
	s.uploader = uploader
	s.enabled = true
	return s
}

// NewImageTaskServiceWithResolver 构造一个由 resolver 决定启用状态的服务：
// 开关与凭证来自后台设置，保存后立即生效，无需重启。
func NewImageTaskServiceWithResolver(store ImageTaskStore, resolve ImageStorageResolver, ttl, executionTimeout time.Duration) *ImageTaskService {
	s := NewImageTaskServiceWithOptions(store, ttl, executionTimeout)
	s.resolve = resolve
	return s
}

// current 返回当前生效的 uploader 与启用状态。
// 注入了 resolver 时以 resolver 为准（后台设置可热切换），否则回落到构造时固定的值。
func (s *ImageTaskService) current() (*ImageResultUploader, bool) {
	if s == nil {
		return nil, false
	}
	if s.resolve != nil {
		return s.resolve()
	}
	return s.uploader, s.enabled
}

// Enabled 表示异步图片任务功能是否可用（总开关 + 凭证齐全）。
// 关闭时 handler 直接返回 404，不创建任务、不写 Redis。
func (s *ImageTaskService) Enabled() bool {
	if s == nil || s.store == nil {
		return false
	}
	_, enabled := s.current()
	return enabled
}

// Pollable 表示已创建的任务能否被查询。
// 比 Enabled 弱：只要 store 可用即可，从而在功能被关掉后仍能取回进行中的任务结果。
func (s *ImageTaskService) Pollable() bool {
	return s != nil && s.store != nil
}

func (s *ImageTaskService) ExecutionTimeout() time.Duration {
	if s == nil || s.executionTimeout <= 0 {
		return defaultImageTaskExecutionTimeout
	}
	return s.executionTimeout
}

func (s *ImageTaskService) Create(ctx context.Context, owner ImageTaskOwner) (*ImageTask, error) {
	if s == nil || s.store == nil {
		return nil, ErrImageTaskUnavailable
	}
	now := time.Now().UTC()
	task := &ImageTaskRecord{
		ID:                  newImageTaskID(ctx),
		ReceiptID:           imageTaskReceiptID(ctx),
		ExecutionDeadlineAt: now.Add(s.ExecutionTimeout()).Unix(),
		UserID:              owner.UserID,
		APIKeyID:            owner.APIKeyID,
		Status:              ImageTaskStatusProcessing,
		CreatedAt:           now.Unix(),
		ExpiresAt:           now.Add(s.ttl).Unix(),
	}
	if err := s.store.Save(ctx, task, s.ttl); err != nil {
		return nil, ErrImageTaskUnavailable.WithCause(err)
	}
	return imageTaskToPublic(task), nil
}

func (s *ImageTaskService) Get(ctx context.Context, owner ImageTaskOwner, id string) (*ImageTask, error) {
	if s == nil || s.store == nil {
		return nil, ErrImageTaskUnavailable
	}
	task, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, ErrImageTaskNotFound) {
			return nil, ErrImageTaskNotFound
		}
		return nil, ErrImageTaskUnavailable.WithCause(err)
	}
	if task.UserID != owner.UserID || task.APIKeyID != owner.APIKeyID {
		// Do not reveal whether a random task ID exists for another caller.
		return nil, ErrImageTaskNotFound
	}
	deadline := task.ExecutionDeadlineAt
	if deadline == 0 {
		deadline = time.Unix(task.CreatedAt, 0).Add(s.ExecutionTimeout()).Unix()
	}
	if task.Status == ImageTaskStatusProcessing && time.Now().Unix() > deadline {
		copy := *task
		copy.Status = ImageTaskStatusUncertain
		copy.Error = imageTaskErrorJSON("execution_interrupted", "Image execution exceeded its deadline or was interrupted; do not resubmit this operation")
		task = &copy
	}
	return imageTaskToPublic(task), nil
}

type doneBotMediaReceiptContextKey struct{}

// WithDoneBotMediaReceipt binds a server-created receipt after installation
// ownership and idempotency have been checked; caller headers are not receipts.
func WithDoneBotMediaReceipt(ctx context.Context, receiptID string) context.Context {
	return context.WithValue(ctx, doneBotMediaReceiptContextKey{}, receiptID)
}

func imageTaskReceiptID(ctx context.Context) string {
	id, _ := ctx.Value(doneBotMediaReceiptContextKey{}).(string)
	if parsed, err := uuid.Parse(id); err == nil {
		return parsed.String()
	}
	return ""
}

func DoneBotImageTaskID(receiptID string) string {
	return "imgtask_" + strings.ReplaceAll(receiptID, "-", "")
}

func newImageTaskID(ctx context.Context) string {
	if receipt := imageTaskReceiptID(ctx); receipt != "" {
		return DoneBotImageTaskID(receipt)
	}
	return DoneBotImageTaskID(uuid.NewString())
}

func (s *ImageTaskService) Complete(ctx context.Context, id string, statusCode int, result json.RawMessage) error {
	_, err := s.CompleteDelivered(ctx, id, statusCode, result)
	return err
}

// CompleteDelivered reports whether the result was durably delivered, so an
// asynchronous caller can commit existing usage jobs without another Redis read.
func (s *ImageTaskService) CompleteDelivered(ctx context.Context, id string, statusCode int, result json.RawMessage) (bool, error) {
	if !json.Valid(result) {
		return false, s.Fail(ctx, id, http.StatusBadGateway, imageTaskErrorJSON("api_error", "upstream returned a non-JSON image response"))
	}
	if uploader, _ := s.current(); uploader != nil {
		// Local result assets use the same API-key ownership as the task. Resolve
		// the owner from persisted metadata rather than request-supplied data.
		task, err := s.store.Get(ctx, id)
		if err != nil {
			if errors.Is(err, ErrImageTaskNotFound) {
				return false, ErrImageTaskNotFound
			}
			return false, ErrImageTaskUnavailable.WithCause(err)
		}
		rewritten, err := uploader.Rewrite(WithAssetOwnerKeyID(ctx, task.APIKeyID), id, result)
		if err != nil {
			// 转存失败不回退存 base64，避免大 blob 撑爆 Redis：直接把任务标记为失败。
			logger.L().Error("image_task.offload_failed", zap.String("task_id", id), zap.Error(err))
			return false, s.Fail(ctx, id, http.StatusBadGateway, imageTaskErrorJSON("api_error", "failed to store generated image to object storage"))
		}
		result = rewritten
	}
	err := s.finish(ctx, id, ImageTaskStatusCompleted, statusCode, result, nil, false)
	return err == nil, err
}

func (s *ImageTaskService) Fail(ctx context.Context, id string, statusCode int, taskErr json.RawMessage) error {
	return s.fail(ctx, id, statusCode, taskErr, false)
}

// FailNotDispatched requires the caller to have confirmed internally that no
// upstream request was attempted; response fields and caller headers are not proof.
func (s *ImageTaskService) FailNotDispatched(ctx context.Context, id string, statusCode int, taskErr json.RawMessage) error {
	return s.fail(ctx, id, statusCode, taskErr, true)
}

func (s *ImageTaskService) fail(ctx context.Context, id string, statusCode int, taskErr json.RawMessage, notDispatched bool) error {
	if !json.Valid(taskErr) {
		taskErr = imageTaskErrorJSON("api_error", "image generation failed")
	}
	return s.finish(ctx, id, ImageTaskStatusFailed, statusCode, nil, taskErr, notDispatched)
}

func (s *ImageTaskService) finish(ctx context.Context, id, status string, statusCode int, result, taskErr json.RawMessage, notDispatched bool) error {
	if s == nil || s.store == nil {
		return ErrImageTaskUnavailable
	}
	task, err := s.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrImageTaskNotFound) {
			return ErrImageTaskNotFound
		}
		return ErrImageTaskUnavailable.WithCause(err)
	}
	if task.ReceiptID != "" && status == ImageTaskStatusFailed && !notDispatched && (statusCode >= http.StatusInternalServerError || statusCode == http.StatusRequestTimeout || statusCode == 499) {
		status = ImageTaskStatusUncertain
	}
	now := time.Now().UTC()
	completedAt := now.Unix()
	task.Status = status
	task.HTTPStatus = statusCode
	task.Result = result
	task.Error = taskErr
	task.CompletedAt = &completedAt
	task.ExpiresAt = now.Add(s.ttl).Unix()
	ttl := s.ttl
	if remaining := time.Unix(task.IdempotencyExpiresAt, 0).Sub(now); remaining > ttl {
		ttl = remaining
		task.ExpiresAt = task.IdempotencyExpiresAt
	}
	if err := s.store.Save(ctx, task, ttl); err != nil {
		return ErrImageTaskUnavailable.WithCause(err)
	}
	return nil
}

func imageTaskToPublic(task *ImageTaskRecord) *ImageTask {
	if task == nil {
		return nil
	}
	return &ImageTask{
		ReceiptID:   task.ReceiptID,
		ID:          task.ID,
		TaskID:      task.ID,
		Object:      "image.generation.task",
		Status:      task.Status,
		HTTPStatus:  task.HTTPStatus,
		ImageURL:    firstImageTaskURL(task.Result),
		Result:      task.Result,
		Error:       task.Error,
		CreatedAt:   task.CreatedAt,
		CompletedAt: task.CompletedAt,
		ExpiresAt:   task.ExpiresAt,
	}
}

func firstImageTaskURL(result json.RawMessage) string {
	if len(result) == 0 || !json.Valid(result) {
		return ""
	}
	var response struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if json.Unmarshal(result, &response) != nil || len(response.Data) == 0 {
		return ""
	}
	return strings.TrimSpace(response.Data[0].URL)
}

func imageTaskErrorJSON(errorType, message string) json.RawMessage {
	data, _ := json.Marshal(map[string]string{"type": errorType, "message": message})
	return data
}
