package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	DoneBotDispatchImages        = "images"
	DoneBotDispatchAPIMartImages = "apimart_images"
	DoneBotDispatchAPIMartMusic  = "apimart_music"
	DoneBotDispatchSeedance      = "seedance"
)

// DoneBotDispatch is a durable logical task. Provider task IDs stay private to its attempts.
type DoneBotDispatchAttempt struct {
	AccountID      int64                   `json:"account_id"`
	ProviderTaskID string                  `json:"provider_task_id,omitempty"`
	Status         string                  `json:"status"`
	Diagnostic     *DoneBotMediaDiagnostic `json:"diagnostic,omitempty"`
}

type DoneBotDispatch struct {
	ReceiptID      string                   `json:"receipt_id"`
	TaskID         string                   `json:"task_id"`
	UserID         int64                    `json:"user_id"`
	APIKeyID       int64                    `json:"api_key_id"`
	GroupID        int64                    `json:"group_id"`
	Protocol       string                   `json:"protocol"`
	Kind           string                   `json:"kind"`
	Model          string                   `json:"model"`
	RoutingModel   string                   `json:"routing_model"`
	ForwardModel   string                   `json:"forward_model"`
	RouteSource    string                   `json:"route_source,omitempty"`
	Path           string                   `json:"path"`
	ContentType    string                   `json:"content_type"`
	Body           []byte                   `json:"body,omitempty"`
	Info           APIMartRequestInfo       `json:"info,omitempty"`
	VideoInfo      GrokMediaRequestInfo     `json:"video_info,omitempty"`
	State          string                   `json:"state"`
	AccountID      int64                    `json:"account_id,omitempty"`
	Attempts       []DoneBotDispatchAttempt `json:"attempts,omitempty"`
	FailedAccounts []int64                  `json:"failed_accounts,omitempty"`
	ProviderTaskID string                   `json:"provider_task_id,omitempty"`
	UpstreamModel  string                   `json:"upstream_model,omitempty"`
	Result         *OpenAIForwardResult     `json:"result,omitempty"`
	Response       []byte                   `json:"response,omitempty"`
	Diagnostic     *DoneBotMediaDiagnostic  `json:"diagnostic,omitempty"`
	CreatedAt      time.Time                `json:"created_at"`
	Lease          string                   `json:"-"`
}

// DoneBotDispatchRepository checkpoints a dispatch before any paid upstream POST.
// An expired dispatching lease is uncertain; only known provider IDs may be polled again.
type DoneBotDispatchRepository interface {
	TakeDoneBotDispatches(context.Context, int) ([]DoneBotDispatch, error)
	SaveDoneBotDispatch(context.Context, *DoneBotDispatch, time.Duration) error
	LookupDoneBotDispatch(context.Context, int64, int64, string) (*DoneBotDispatch, error)
	ReassignDoneBotMediaAccount(context.Context, string, int64, int64, float64) error
}

type doneBotDispatchContextKey struct{}

func WithDoneBotDispatch(ctx context.Context, task *DoneBotDispatch) context.Context {
	return context.WithValue(ctx, doneBotDispatchContextKey{}, task)
}

func DoneBotDispatchFromContext(ctx context.Context) *DoneBotDispatch {
	task, _ := ctx.Value(doneBotDispatchContextKey{}).(*DoneBotDispatch)
	return task
}

func (s *OpenAIGatewayService) doneBotDispatchRepository() DoneBotDispatchRepository {
	repo, _ := s.usageBillingRepo.(DoneBotDispatchRepository)
	return repo
}

func (s *OpenAIGatewayService) SubmitDoneBotDispatch(ctx context.Context, key *APIKey, account *Account, task DoneBotDispatch) (*DoneBotDispatch, error) {
	repo := s.doneBotDispatchRepository()
	receipt := DoneBotMediaReceiptID(ctx)
	if repo == nil || receipt == "" {
		return nil, ErrDoneBotMediaPrepaidUnavailable
	}
	task.ReceiptID = receipt
	task.TaskID = "dbtask_" + strings.ReplaceAll(receipt, "-", "")
	if task.Protocol == DoneBotDispatchImages {
		task.TaskID = DoneBotImageTaskID(receipt)
	}
	if key == nil || key.Group == nil || account == nil {
		return nil, ErrDoneBotMediaInvalid
	}
	task.UserID, task.APIKeyID, task.GroupID = key.UserID, key.ID, key.Group.ID
	task.Model = firstNonEmpty(task.Model, task.RoutingModel)
	task.ForwardModel = firstNonEmpty(task.ForwardModel, task.RoutingModel)
	task.CreatedAt = time.Now().UTC()
	task.State = "queued"
	task.AccountID = account.ID
	task.RouteSource, _ = CompositeRouteSourceFromContext(ctx)
	if err := task.validate(); err != nil {
		return nil, err
	}
	ctx = WithDoneBotDispatch(ctx, &task)
	var err error
	switch task.Protocol {
	case DoneBotDispatchImages:
		err = s.PrepayDoneBotImage(ctx, key, account, task.Model, int(task.Info.Units))
	case DoneBotDispatchAPIMartImages, DoneBotDispatchAPIMartMusic:
		endpoint := APIMartImages
		if task.Protocol == DoneBotDispatchAPIMartMusic {
			endpoint = APIMartSuno
		}
		err = s.PrepayDoneBotAPIMart(ctx, key, account, task.Info, endpoint)
	case DoneBotDispatchSeedance:
		err = s.PrepayDoneBotSeedance(ctx, key, account, task.Model, task.VideoInfo)
	default:
		err = ErrDoneBotMediaInvalid
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (task *DoneBotDispatch) AcceptedBody() []byte {
	var value any = map[string]any{"id": task.TaskID, "task_id": task.TaskID, "status": "processing", "receipt_id": task.ReceiptID}
	if task.Protocol == DoneBotDispatchAPIMartImages || task.Protocol == DoneBotDispatchAPIMartMusic {
		value = map[string]any{"code": 200, "data": []any{map[string]any{"task_id": task.TaskID, "status": "processing"}}}
	}
	body, _ := json.Marshal(value)
	return body
}

func (task *DoneBotDispatch) PublicBody() []byte {
	status := "processing"
	if task.State == "completed" {
		status = "completed"
	}
	if task.State == "failed" {
		status = "failed"
	}
	if task.State == "uncertain" {
		status = "uncertain"
	}
	if task.Protocol == DoneBotDispatchImages {
		value := map[string]any{"id": task.TaskID, "task_id": task.TaskID, "status": status, "receipt_id": task.ReceiptID}
		if task.State == "completed" {
			value["result"] = json.RawMessage(task.Response)
		}
		if status == "failed" || status == "uncertain" {
			value["error"] = task.publicError()
		}
		body, _ := json.Marshal(value)
		return body
	}
	var value map[string]any
	if task.State == "completed" {
		_ = json.Unmarshal(task.Response, &value)
	}
	if value == nil {
		value = map[string]any{}
	}
	target := value
	if task.Protocol != DoneBotDispatchSeedance {
		if data, ok := value["data"].(map[string]any); ok {
			target = data
		} else {
			target = map[string]any{}
			value["data"] = target
		}
	} else if status == "completed" {
		status = "succeeded"
	}
	target["id"], target["task_id"], target["status"] = task.TaskID, task.TaskID, status
	if status == "failed" || status == "uncertain" {
		target["error"] = task.publicError()
	}
	body, _ := json.Marshal(value)
	return body
}

func (task *DoneBotDispatch) publicError() map[string]any {
	message := "All eligible upstream accounts failed"
	code := "upstream_accounts_exhausted"
	if task.State == "uncertain" {
		message = "Upstream acceptance is unconfirmed; query this task instead of resubmitting"
		code = "dispatch_unconfirmed"
	}
	if task.Diagnostic != nil && task.Diagnostic.Message != "" {
		message = task.Diagnostic.Message
	}
	return map[string]any{"type": "upstream_error", "code": code, "message": message}
}

func (s *OpenAIGatewayService) LookupDoneBotDispatch(ctx context.Context, userID, keyID int64, id string) (*DoneBotDispatch, error) {
	if !strings.HasPrefix(id, "dbtask_") && !strings.HasPrefix(id, "imgtask_") {
		return nil, ErrDoneBotMediaNotFound
	}
	repo := s.doneBotDispatchRepository()
	if repo == nil {
		return nil, ErrDoneBotMediaPrepaidUnavailable
	}
	task, err := repo.LookupDoneBotDispatch(ctx, userID, keyID, id)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (task *DoneBotDispatch) failed(accountID int64, diagnostic *DoneBotMediaDiagnostic) {
	task.Attempts = append(task.Attempts, DoneBotDispatchAttempt{AccountID: accountID, ProviderTaskID: task.ProviderTaskID, Status: "failed", Diagnostic: diagnostic})
	task.FailedAccounts = append(task.FailedAccounts, accountID)
	task.ProviderTaskID = ""
	task.State = "queued"
	task.Diagnostic = diagnostic
}

func (task *DoneBotDispatch) validate() error {
	if task.ReceiptID == "" || task.TaskID == "" || task.UserID <= 0 || task.APIKeyID <= 0 || task.GroupID <= 0 || len(task.Body) == 0 {
		return ErrDoneBotMediaInvalid
	}
	if _, err := uuid.Parse(task.ReceiptID); err != nil {
		return ErrDoneBotMediaInvalid
	}
	return nil
}

// DoneBotExplicitMediaRejection distinguishes a rejected create from an unconfirmed response.
func DoneBotExplicitMediaRejection(status int, body []byte) bool {
	if doneBotKnownRejection(status) {
		return true
	}
	if status >= 200 && status < 300 {
		code := gjson.GetBytes(body, "code").Int()
		return doneBotKnownRejection(int(code))
	}
	return false
}

func doneBotKnownRejection(status int) bool {
	return status >= http.StatusBadRequest && status < http.StatusInternalServerError && status != http.StatusRequestTimeout && status != 499
}

var ErrDoneBotMediaDispatchSpacing = errors.New("media dispatch interval has not elapsed")

var errDoneBotDispatchLease = errors.New("media dispatch lease is no longer owned")

func DoneBotDispatchLeaseError() error { return errDoneBotDispatchLease }
