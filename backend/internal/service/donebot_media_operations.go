package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const DoneBotMediaReplayLimit = 2 << 20

var (
	ErrDoneBotMediaNotFound  = infraerrors.NotFound("DONEBOT_MEDIA_NOT_FOUND", "media operation not found")
	ErrDoneBotMediaConflict  = infraerrors.Conflict("DONEBOT_MEDIA_IDEMPOTENCY_CONFLICT", "idempotency key belongs to a different media request")
	ErrDoneBotMediaPending   = infraerrors.Conflict("DONEBOT_MEDIA_IN_PROGRESS", "media submission may already have reached the provider; query its receipt instead of submitting again")
	ErrDoneBotMediaInvalid   = infraerrors.BadRequest("DONEBOT_MEDIA_INVALID", "invalid media operation")
	doneBotMediaHash         = regexp.MustCompile(`^[a-f0-9]{64}$`)
	doneBotMediaIdentifier   = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,600}$`)
	doneBotMediaCredential   = regexp.MustCompile(`(?i)(^|[^a-z0-9])sk-[a-z0-9]`)
	doneBotMediaBase64       = regexp.MustCompile(`^[A-Za-z0-9+/=\r\n]*$`)
	doneBotMediaRetrySeconds = regexp.MustCompile(`^[0-9]{1,10}$`)
)

// DoneBotMediaOwner is resolved from the authenticated API key, never from a request header.
type DoneBotMediaOwner struct {
	UserID         int64
	APIKeyID       int64
	GroupID        int64
	InstallationID string
}

type DoneBotMediaOperation struct {
	ReceiptID         string
	Owner             DoneBotMediaOwner
	IdempotencyHash   string
	Fingerprint       string
	Kind              string
	Model             string
	Method            string
	Path              string
	Status            string
	TaskID            string
	TaskKey           string
	Provider          string
	UsageRequestID    string
	EstimatedCents    *json.Number
	ReplayStatus      int
	ReplayContentType string
	ReplayRetryAfter  string
	ReplayBody        []byte
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type DoneBotMediaBeginInput struct {
	Owner          DoneBotMediaOwner
	IdempotencyKey string
	Fingerprint    string
	Kind           string
	Model          string
	Method         string
	Path           string
	EstimatedCents *json.Number
}

type DoneBotMediaBeginResult struct {
	Operation *DoneBotMediaOperation
	Replay    bool
}

type DoneBotMediaCompletion struct {
	Status         string
	TaskID         string
	TaskKey        string
	Provider       string
	UsageRequestID string
	HTTPStatus     int
	ContentType    string
	RetryAfter     string
	Body           []byte
}

type DoneBotMediaOperationRepository interface {
	Begin(context.Context, *DoneBotMediaOperation) (*DoneBotMediaOperation, bool, error)
	Complete(context.Context, DoneBotMediaOwner, string, DoneBotMediaCompletion) (*DoneBotMediaOperation, error)
	Lookup(context.Context, DoneBotMediaOwner, string) (*DoneBotMediaOperation, error)
	LookupTask(context.Context, DoneBotMediaOwner, string, string) (*DoneBotMediaOperation, error)
	SetEstimate(context.Context, DoneBotMediaOwner, string, *json.Number) error
}

type doneBotMediaUsageReader interface {
	ListWithFilters(context.Context, pagination.PaginationParams, usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error)
}

type DoneBotMediaService struct {
	repo     DoneBotMediaOperationRepository
	installs *DoneBotService
	usage    doneBotMediaUsageReader
	prepaid  DoneBotMediaPrepaidRepository
}

func NewDoneBotMediaService(repo DoneBotMediaOperationRepository, installs *DoneBotService, usage *UsageService, billing UsageBillingRepository) *DoneBotMediaService {
	var reader doneBotMediaUsageReader
	if usage != nil {
		reader = usage
	}
	prepaid, _ := billing.(DoneBotMediaPrepaidRepository)
	return &DoneBotMediaService{repo: repo, installs: installs, usage: reader, prepaid: prepaid}
}

func (s *DoneBotMediaService) ResolveOwner(ctx context.Context, userID, keyID, groupID int64) (DoneBotMediaOwner, error) {
	owner, err := s.resolveOwnerBinding(ctx, DoneBotMediaOwner{UserID: userID, APIKeyID: keyID, GroupID: groupID})
	if err != nil {
		return DoneBotMediaOwner{}, err
	}
	if err := s.installs.enabled(); err != nil {
		return DoneBotMediaOwner{}, err
	}
	_, err = s.installs.InstallationForUser(ctx, userID, owner.InstallationID)
	return owner, err
}

func (s *DoneBotMediaService) resolveOwnerBinding(ctx context.Context, owner DoneBotMediaOwner) (DoneBotMediaOwner, error) {
	if s == nil || s.installs == nil || s.repo == nil {
		return DoneBotMediaOwner{}, infraerrors.ServiceUnavailable("DONEBOT_MEDIA_UNAVAILABLE", "media recovery is unavailable")
	}
	if owner.UserID <= 0 || owner.APIKeyID <= 0 || owner.GroupID <= 0 {
		return DoneBotMediaOwner{}, ErrDoneBotInstallNotFound
	}
	repo, ok := s.installs.repo.(interface {
		GetByAPIKeyID(context.Context, int64) (*DoneBotInstall, error)
	})
	if !ok {
		return DoneBotMediaOwner{}, infraerrors.ServiceUnavailable("DONEBOT_MEDIA_UNAVAILABLE", "media recovery is unavailable")
	}
	install, err := repo.GetByAPIKeyID(ctx, owner.APIKeyID)
	if err != nil {
		return DoneBotMediaOwner{}, err
	}
	if install == nil || install.UserID != owner.UserID || install.APIKeyID != owner.APIKeyID || install.GroupID != owner.GroupID {
		return DoneBotMediaOwner{}, ErrDoneBotInstallNotFound
	}
	if owner.InstallationID != "" && owner.InstallationID != install.InstallationID {
		return DoneBotMediaOwner{}, ErrDoneBotMediaNotFound
	}
	owner.InstallationID = install.InstallationID
	return owner, nil
}

func (s *DoneBotMediaService) checkedOwner(ctx context.Context, owner DoneBotMediaOwner) (DoneBotMediaOwner, error) {
	resolved, err := s.ResolveOwner(ctx, owner.UserID, owner.APIKeyID, owner.GroupID)
	if err != nil {
		return DoneBotMediaOwner{}, err
	}
	if owner.InstallationID != "" && owner.InstallationID != resolved.InstallationID {
		return DoneBotMediaOwner{}, ErrDoneBotMediaNotFound
	}
	return resolved, nil
}

func DoneBotMediaFingerprint(method, path, contentType string, body []byte) string {
	h := sha256.New()
	for _, field := range []string{method, path, contentType} {
		_, _ = h.Write([]byte(field))
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func (s *DoneBotMediaService) Begin(ctx context.Context, input DoneBotMediaBeginInput) (*DoneBotMediaBeginResult, error) {
	owner, err := s.checkedOwner(ctx, input.Owner)
	if err != nil {
		return nil, err
	}
	if !doneBotMediaHash.MatchString(input.Fingerprint) || !doneBotMediaPath(input.Kind, input.Method, input.Path) || !safeMediaModel(input.Model) {
		return nil, ErrDoneBotMediaInvalid
	}
	key := input.IdempotencyKey
	if len(key) == 0 || len(key) > 256 || strings.TrimSpace(key) != key {
		return nil, infraerrors.BadRequest("DONEBOT_IDEMPOTENCY_KEY_REQUIRED", "a nonempty Idempotency-Key of at most 256 characters is required")
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return nil, ErrDoneBotMediaInvalid
		}
	}
	if !doneBotMediaValidEstimate(input.EstimatedCents) {
		return nil, ErrDoneBotMediaInvalid
	}
	id := uuid.NewString()
	hash := sha256.Sum256([]byte(key))
	op := &DoneBotMediaOperation{ReceiptID: id, Owner: owner, IdempotencyHash: hex.EncodeToString(hash[:]), Fingerprint: input.Fingerprint,
		Kind: input.Kind, Model: input.Model, Method: input.Method, Path: input.Path, Status: "dispatching", UsageRequestID: "client:" + id, EstimatedCents: input.EstimatedCents}
	if input.Kind == "speech" {
		op.UsageRequestID = DoneBotSpeechBillingRequestID(id)
	}
	if input.Kind == "image" && strings.HasSuffix(input.Path, "/async") {
		op.TaskID, op.TaskKey, op.Provider = DoneBotImageTaskID(id), DoneBotImageTaskID(id), "donebot"
	}
	op, created, err := s.repo.Begin(ctx, op)
	if err != nil {
		return nil, err
	}
	result := &DoneBotMediaBeginResult{Operation: op}
	if op.Fingerprint != input.Fingerprint || op.Kind != input.Kind || op.Method != input.Method || op.Path != input.Path {
		return result, ErrDoneBotMediaConflict
	}
	if created {
		return result, nil
	}
	if len(op.ReplayBody) != 0 && op.ReplayStatus >= 200 && op.Status != "dispatching" && op.Status != "uncertain" {
		result.Replay = true
		return result, nil
	}
	return result, ErrDoneBotMediaPending
}

func DoneBotSpeechBillingRequestID(receiptID string) string {
	return "apimart:tts:donebot:" + receiptID
}

func doneBotMediaValidEstimate(value *json.Number) bool {
	if value == nil {
		return true
	}
	if len(value.String()) > 64 {
		return false
	}
	amount, err := decimal.NewFromString(value.String())
	return err == nil && !amount.IsNegative() && amount.Exponent() >= -10 && amount.Exponent() <= 20 && len(amount.Coefficient().String()) <= 30 && len(amount.StringFixed(10)) <= 31
}

func (s *DoneBotMediaService) SetEstimate(ctx context.Context, owner DoneBotMediaOwner, receiptID string, amount *json.Number) error {
	owner, err := s.checkedOwner(ctx, owner)
	if err != nil {
		return err
	}
	if _, err := uuid.Parse(receiptID); err != nil {
		return ErrDoneBotMediaNotFound
	}
	if !doneBotMediaValidEstimate(amount) {
		return ErrDoneBotMediaInvalid
	}
	return s.repo.SetEstimate(ctx, owner, receiptID, amount)
}

func doneBotMediaPath(kind, method, path string) bool {
	if method != "POST" {
		return false
	}
	switch kind {
	case "image":
		return path == "/v1/images/generations" || path == "/v1/images/edits" || path == "/v1/images/generations/async" || path == "/v1/images/edits/async"
	case "video":
		return path == "/v1/videos" || path == "/v1/videos/edits" || path == "/v1/videos/extensions"
	case "music":
		return path == "/v1/music/generations"
	case "speech":
		return path == "/v1/audio/speech"
	}
	return false
}

func safeMediaModel(model string) bool {
	return len(model) <= 256 && (model == "" || doneBotMediaIdentifier.MatchString(model)) && !doneBotMediaCredential.MatchString(model)
}

func (s *DoneBotMediaService) Complete(ctx context.Context, owner DoneBotMediaOwner, receiptID string, completion DoneBotMediaCompletion) (*DoneBotMediaOperation, error) {
	owner, err := s.resolveOwnerBinding(ctx, owner)
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(receiptID); err != nil {
		return nil, ErrDoneBotMediaNotFound
	}
	if !doneBotMediaState(completion.Status) || !safeMediaIdentifier(completion.TaskID, 512) || !safeMediaIdentifier(completion.TaskKey, 600) || !safeMediaIdentifier(completion.Provider, 32) || !safeMediaIdentifier(completion.UsageRequestID, 256) {
		return nil, ErrDoneBotMediaInvalid
	}
	if completion.HTTPStatus != 0 && (completion.HTTPStatus < 200 || completion.HTTPStatus > 599) {
		return nil, ErrDoneBotMediaInvalid
	}
	if completion.HTTPStatus == 408 || completion.HTTPStatus == 499 {
		completion.Status = "uncertain"
	}
	completion.RetryAfter = doneBotMediaRetryAfter(completion.RetryAfter)
	if completion.Body != nil {
		body, contentType, ok := doneBotMediaSafeResponse(completion.ContentType, completion.Body)
		completion.Body, completion.ContentType = body, contentType
		if !ok && completion.TaskID == "" {
			completion.Status = "uncertain"
		}
	}
	streamSettled := completion.Provider == "apimart" && strings.HasPrefix(completion.UsageRequestID, "apimart:tts:")
	if completion.HTTPStatus != 0 && completion.Body == nil && completion.TaskID == "" && completion.Status == "completed" && !streamSettled {
		completion.Status = "uncertain"
	}
	if s.prepaid != nil {
		p, err := s.prepaid.LookupDoneBotMediaPrecharge(ctx, receiptID, owner.APIKeyID, "")
		if err != nil {
			return nil, err
		}
		if p != nil {
			completion.UsageRequestID = ""
			if p.State == "prepaid" && completion.Status == "completed" {
				completion.Status = "uncertain"
			}
			op, err := s.repo.Complete(ctx, owner, receiptID, completion)
			if err != nil {
				return nil, err
			}
			if p.State == "prepaid" && (completion.Status == "failed" || completion.Status == "cancelled") {
				_, err = s.prepaid.SettleDoneBotMediaPrecharge(ctx, DoneBotMediaPrepaidSettlement{ReceiptID: receiptID, Failure: true})
				if err != nil {
					return nil, err
				}
				return s.repo.Lookup(ctx, owner, receiptID)
			}
			return op, nil
		}
	}
	return s.repo.Complete(ctx, owner, receiptID, completion)
}

func doneBotMediaRetryAfter(value string) string {
	if len(value) == 0 || len(value) > 64 {
		return ""
	}
	if doneBotMediaRetrySeconds.MatchString(value) {
		return value
	}
	if date, err := http.ParseTime(value); err == nil {
		return date.UTC().Format(http.TimeFormat)
	}
	return ""
}

func safeMediaIdentifier(value string, max int) bool {
	return value == "" || (len(value) <= max && doneBotMediaIdentifier.MatchString(value) && !doneBotMediaCredential.MatchString(value))
}

func doneBotMediaState(state string) bool {
	switch state {
	case "accepted", "processing", "completed", "failed", "cancelled", "refunded", "uncertain":
		return true
	}
	return false
}

func (s *DoneBotMediaService) Lookup(ctx context.Context, owner DoneBotMediaOwner, receiptID string) (*DoneBotMediaOperation, error) {
	owner, err := s.checkedOwner(ctx, owner)
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(receiptID); err != nil {
		return nil, ErrDoneBotMediaNotFound
	}
	return s.repo.Lookup(ctx, owner, receiptID)
}

func (s *DoneBotMediaService) LookupAcceptedTask(ctx context.Context, owner DoneBotMediaOwner, taskID, kind string) (*DoneBotMediaOperation, error) {
	owner, err := s.checkedOwner(ctx, owner)
	if err != nil {
		return nil, err
	}
	if taskID == "" || !safeMediaIdentifier(taskID, 600) {
		return nil, ErrDoneBotMediaNotFound
	}
	return s.repo.LookupTask(ctx, owner, taskID, kind)
}

func (s *DoneBotMediaService) Observe(ctx context.Context, owner DoneBotMediaOwner, taskID, kind, status string) (*DoneBotMediaOperation, error) {
	op, err := s.LookupAcceptedTask(ctx, owner, taskID, kind)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending", "submitted", "accepted":
		status = "accepted"
	case "running", "in_progress", "processing":
		status = "processing"
	case "succeeded", "success", "completed", "done":
		status = "completed"
	case "failed", "error", "expired":
		status = "failed"
	case "cancelled", "canceled":
		status = "cancelled"
	case "refunded":
		status = "refunded"
	case "uncertain":
		status = "uncertain"
	default:
		return op, nil
	}
	return s.Complete(ctx, op.Owner, op.ReceiptID, DoneBotMediaCompletion{Status: status})
}

// DoneBotMediaReceipt separates pending prepayment from confirmed settlement.
type DoneBotMediaReceipt struct {
	PaymentState       string       `json:"payment_state,omitempty"`
	PrepaidCents       *json.Number `json:"prepaid_cents,omitempty"`
	RefundedCents      *json.Number `json:"refunded_cents,omitempty"`
	ReceiptID          string       `json:"receipt_id"`
	RequestID          string       `json:"request_id"`
	InstallationID     string       `json:"installation_id"`
	Status             string       `json:"status"`
	MediaType          string       `json:"media_type"`
	Model              string       `json:"model"`
	TaskID             string       `json:"task_id,omitempty"`
	Currency           string       `json:"currency"`
	EstimatedCents     *json.Number `json:"estimated_cents"`
	ActualCents        *json.Number `json:"actual_cents"`
	RequestedReasoning *string      `json:"requested_reasoning"`
	EffectiveReasoning *string      `json:"effective_reasoning"`
}

func (s *DoneBotMediaService) Receipt(ctx context.Context, userID int64, installationID, receiptID string) (*DoneBotMediaReceipt, error) {
	if s == nil || s.installs == nil || s.repo == nil || s.usage == nil {
		return nil, infraerrors.ServiceUnavailable("DONEBOT_MEDIA_UNAVAILABLE", "media recovery is unavailable")
	}
	install, err := s.installs.InstallationForUser(ctx, userID, installationID)
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(receiptID); err != nil {
		return nil, ErrDoneBotMediaNotFound
	}
	owner := DoneBotMediaOwner{UserID: userID, APIKeyID: install.APIKeyID, GroupID: install.GroupID, InstallationID: install.InstallationID}
	op, err := s.repo.Lookup(ctx, owner, receiptID)
	if err != nil {
		return nil, err
	}
	if s.installs.cfg.DoneBot.CatalogCurrency != "CNY" {
		return nil, ErrDoneBotCatalogCurrency
	}
	receipt := &DoneBotMediaReceipt{ReceiptID: op.ReceiptID, RequestID: op.UsageRequestID, InstallationID: owner.InstallationID, Status: op.Status,
		MediaType: op.Kind, Model: op.Model, TaskID: op.TaskID, Currency: "CNY", EstimatedCents: op.EstimatedCents}
	if s.prepaid != nil {
		p, err := s.prepaid.LookupDoneBotMediaPrecharge(ctx, receiptID, owner.APIKeyID, "")
		if err != nil {
			return nil, err
		}
		if p != nil {
			paid, err := doneBotCents(p.Amount)
			if err != nil {
				return nil, err
			}
			refunded, err := doneBotCents(p.RefundedAmount)
			if err != nil {
				return nil, err
			}
			if p.State == "prepaid" && op.Kind == "image" && op.Provider == "donebot" && !op.CreatedAt.IsZero() && time.Since(op.CreatedAt) > defaultImageTaskExecutionTimeout {
				receipt.Status = "uncertain"
			}
			receipt.RequestID = DoneBotMediaChargeRequestID(receiptID)
			receipt.PaymentState = p.State
			receipt.PrepaidCents = &paid
			receipt.RefundedCents = &refunded
			if p.State == "settled" || p.State == "refunded" {
				actual, err := doneBotCents(p.SettledAmount)
				if err != nil {
					return nil, err
				}
				receipt.ActualCents = &actual
				if p.State == "refunded" {
					receipt.Status = "refunded"
				} else {
					receipt.Status = "completed"
				}
			}
			return receipt, nil
		}
	}
	logs, _, err := s.usage.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 1}, usagestats.UsageLogFilters{UserID: userID, APIKeyID: owner.APIKeyID, GroupID: owner.GroupID, RequestID: op.UsageRequestID})
	if err != nil {
		return nil, err
	}
	if len(logs) == 0 {
		return receipt, nil
	}
	log := &logs[0]
	if log.UserID != owner.UserID || log.APIKeyID != owner.APIKeyID || log.GroupID == nil || *log.GroupID != owner.GroupID || log.RequestID != op.UsageRequestID {
		return nil, ErrDoneBotMediaNotFound
	}
	actual, err := doneBotCents(log.ActualCost)
	if err != nil {
		return nil, err
	}
	receipt.ActualCents, receipt.RequestedReasoning, receipt.EffectiveReasoning = &actual, log.RequestedReasoningEffort, log.ReasoningEffort
	if receipt.Status == "accepted" || receipt.Status == "processing" || receipt.Status == "dispatching" || receipt.Status == "uncertain" {
		receipt.Status = "completed"
	}
	return receipt, nil
}

func doneBotMediaSafeResponse(contentType string, body []byte) ([]byte, string, bool) {
	if len(body) == 0 || len(body) > DoneBotMediaReplayLimit {
		return nil, "", false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, "", false
	}
	if strings.HasPrefix(mediaType, "audio/") || mediaType == "application/octet-stream" {
		return bytes.Clone(body), mediaType, true
	}
	if mediaType != "application/json" || bytes.Contains(bytes.ToLower(body), []byte("sk-donebot-")) {
		return nil, "", false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, "", false
	}
	clean := doneBotMediaSafeJSON(value, 0)
	encoded, err := json.Marshal(clean)
	if err != nil || clean == nil || string(encoded) == "{}" {
		return nil, "", false
	}
	return encoded, mediaType, true
}

func doneBotMediaSafeJSON(value any, depth int) any {
	if depth > 8 {
		return nil
	}
	switch value := value.(type) {
	case map[string]any:
		clean := make(map[string]any)
		for key, item := range value {
			switch key {
			case "id", "task_id", "request_id", "receipt_id", "object", "status", "model", "code", "type", "created", "created_at", "updated_at", "completed_at", "expires_at", "progress", "duration", "seconds", "size", "resolution", "output_format", "seed":
				switch scalar := item.(type) {
				case json.Number, bool, nil:
					clean[key] = scalar
				case string:
					if len(scalar) <= 600 && !doneBotMediaCredential.MatchString(scalar) {
						clean[key] = scalar
					}
				}
			case "message":
				clean[key] = "Media request could not be completed."
			case "data", "result", "error", "images", "videos", "audio", "music", "outputs":
				if child := doneBotMediaSafeJSON(item, depth+1); child != nil {
					clean[key] = child
				}
			case "url", "audio_url", "video_url", "image_url", "poll_url":
				if target, ok := item.(string); ok && doneBotMediaOutputURL(target) {
					clean[key] = target
				}
			case "b64_json":
				if encoded, ok := item.(string); ok && len(encoded) <= DoneBotMediaReplayLimit && doneBotMediaBase64.MatchString(encoded) {
					clean[key] = encoded
				}
			}
		}
		return clean
	case []any:
		if len(value) > 100 {
			return nil
		}
		clean := make([]any, 0, len(value))
		for _, item := range value {
			if child := doneBotMediaSafeJSON(item, depth+1); child != nil {
				clean = append(clean, child)
			}
		}
		return clean
	}
	return nil
}

func doneBotMediaOutputURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "" {
		return strings.HasPrefix(parsed.Path, "/v1/") && parsed.Host == "" && !strings.HasPrefix(raw, "//") && parsed.RawQuery == ""
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}
	for key := range parsed.Query() {
		if strings.Contains(strings.ToLower(key), "key") || strings.EqualFold(key, "authorization") {
			return false
		}
	}
	return !doneBotMediaCredential.MatchString(raw)
}
