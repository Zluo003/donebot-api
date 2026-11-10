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
	DoneBotDispatchImages         = "images"
	DoneBotDispatchAPIMartImages  = "apimart_images"
	DoneBotDispatchAPIMartMusic   = "apimart_music"
	DoneBotDispatchSeedance       = "seedance"
	DoneBotDispatchCandidateLimit = 3
)

// DoneBotDispatch is a durable logical task. Provider task IDs stay private to its attempts.
type DoneBotDispatchAttempt struct {
	PublicModel    string                  `json:"public_model"`
	AccountID      int64                   `json:"account_id"`
	UpstreamModel  string                  `json:"upstream_model,omitempty"`
	ProviderTaskID string                  `json:"provider_task_id,omitempty"`
	Status         string                  `json:"status"`
	Diagnostic     *DoneBotMediaDiagnostic `json:"diagnostic,omitempty"`
}

type DoneBotDispatchCandidate struct {
	PublicModel  string                        `json:"public_model"`
	RoutingModel string                        `json:"routing_model"`
	BillingModel string                        `json:"billing_model"`
	ForwardModel string                        `json:"forward_model"`
	Kind         string                        `json:"kind"`
	Protocol     string                        `json:"protocol"`
	Path         string                        `json:"path"`
	ContentType  string                        `json:"content_type"`
	Body         []byte                        `json:"body"`
	Info         APIMartRequestInfo            `json:"info,omitempty"`
	VideoInfo    GrokMediaRequestInfo          `json:"video_info,omitempty"`
	References   []DoneBotMediaIntentReference `json:"references,omitempty"`
}

type DoneBotMediaArtifact struct {
	ID        string    `json:"id"`
	AssetID   string    `json:"asset_id"`
	Kind      string    `json:"kind"`
	MIME      string    `json:"mime"`
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expires_at"`
}

type DoneBotDispatch struct {
	ReceiptID       string                        `json:"receipt_id"`
	TaskID          string                        `json:"task_id"`
	UserID          int64                         `json:"user_id"`
	APIKeyID        int64                         `json:"api_key_id"`
	GroupID         int64                         `json:"group_id"`
	Protocol        string                        `json:"protocol"`
	Kind            string                        `json:"kind"`
	Model           string                        `json:"model"`
	RoutingModel    string                        `json:"routing_model"`
	ForwardModel    string                        `json:"forward_model"`
	RouteSource     string                        `json:"route_source,omitempty"`
	Path            string                        `json:"path"`
	ContentType     string                        `json:"content_type"`
	Body            []byte                        `json:"body,omitempty"`
	Info            APIMartRequestInfo            `json:"info,omitempty"`
	VideoInfo       GrokMediaRequestInfo          `json:"video_info,omitempty"`
	BillingModel    string                        `json:"billing_model,omitempty"`
	Candidates      []DoneBotDispatchCandidate    `json:"candidates,omitempty"`
	CandidateIndex  int                           `json:"candidate_index,omitempty"`
	DoneBotIntent   bool                          `json:"donebot_intent,omitempty"`
	IntentKind      string                        `json:"intent_kind,omitempty"`
	IntentOperation string                        `json:"intent_operation,omitempty"`
	References      []DoneBotMediaIntentReference `json:"references,omitempty"`
	Artifacts       []DoneBotMediaArtifact        `json:"artifacts,omitempty"`
	State           string                        `json:"state"`
	AccountID       int64                         `json:"account_id,omitempty"`
	Attempts        []DoneBotDispatchAttempt      `json:"attempts,omitempty"`
	FailedAccounts  []int64                       `json:"failed_accounts,omitempty"`
	ProviderTaskID  string                        `json:"provider_task_id,omitempty"`
	UpstreamModel   string                        `json:"upstream_model,omitempty"`
	Result          *OpenAIForwardResult          `json:"result,omitempty"`
	Response        []byte                        `json:"response,omitempty"`
	Diagnostic      *DoneBotMediaDiagnostic       `json:"diagnostic,omitempty"`
	CreatedAt       time.Time                     `json:"created_at"`
	Lease           string                        `json:"-"`
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
	if key == nil || key.Group == nil {
		return nil, ErrDoneBotMediaInvalid
	}
	task.UserID, task.APIKeyID, task.GroupID = key.UserID, key.ID, key.Group.ID
	task.Model = firstNonEmpty(task.Model, task.RoutingModel)
	task.BillingModel = firstNonEmpty(task.BillingModel, task.Model)
	task.ForwardModel = firstNonEmpty(task.ForwardModel, task.RoutingModel)
	if len(task.Candidates) > 0 {
		if task.CandidateIndex != 0 || len(task.Candidates) > DoneBotDispatchCandidateLimit {
			return nil, ErrDoneBotMediaInvalid
		}
		for i := range task.Candidates {
			candidateTask := task
			if err := candidateTask.activateCandidate(i); err != nil {
				return nil, err
			}
		}
		if err := task.activateCandidate(0); err != nil {
			return nil, err
		}
	}
	var releaseSelection func()
	if account == nil {
		capability, imageCapability, capabilityErr := doneBotDispatchRequiredCapabilities(ctx, s, &task)
		if capabilityErr != nil {
			return nil, capabilityErr
		}
		selection, _, selectErr := s.selectAccountWithScheduler(WithDoneBotPriority(ctx), &task.GroupID, "", "", task.RoutingModel, nil,
			OpenAIUpstreamTransportHTTPSSE, capability, imageCapability, false, PlatformOpenAI, false, false)
		if selectErr != nil {
			return nil, selectErr
		}
		if selection == nil || selection.Account == nil {
			return nil, ErrNoAvailableAccounts
		}
		account = selection.Account
		releaseSelection = selection.ReleaseFunc
	}
	if releaseSelection != nil {
		defer releaseSelection()
	}
	task.CreatedAt = time.Now().UTC()
	task.State = "queued"
	task.AccountID = account.ID
	task.RouteSource, _ = CompositeRouteSourceFromContext(ctx)
	if err := task.validate(); err != nil {
		return nil, err
	}
	ctx = WithDoneBotDispatch(ctx, &task)
	var err error
	if len(task.Candidates) > 0 {
		var snapshot DoneBotMediaPriceSnapshot
		snapshot, err = s.doneBotMediaCandidatesPriceSnapshot(ctx, key, account, task.Candidates)
		if err == nil {
			err = s.prepayDoneBotMedia(ctx, key, account, task.Model, snapshot)
		}
	} else {
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
			if task.DoneBotIntent {
				value["artifacts"] = task.publicArtifacts()
			} else {
				value["result"] = json.RawMessage(task.Response)
			}
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
	if task.DoneBotIntent && status == "completed" {
		target["artifacts"] = task.publicArtifacts()
		delete(target, "result")
	}
	target["id"], target["task_id"], target["status"] = task.TaskID, task.TaskID, status
	if status == "failed" || status == "uncertain" {
		target["error"] = task.publicError()
	}
	body, _ := json.Marshal(value)
	return body
}

func DoneBotMediaArtifactURL(taskID, artifactID string) string {
	return "/v1/donebot/media/jobs/" + taskID + "/artifacts/" + artifactID
}

func (task *DoneBotDispatch) publicArtifacts() []DoneBotMediaIntentArtifact {
	artifacts := make([]DoneBotMediaIntentArtifact, 0, len(task.Artifacts))
	for _, artifact := range task.Artifacts {
		artifacts = append(artifacts, DoneBotMediaIntentArtifact{ID: artifact.ID, Kind: artifact.Kind, MIME: artifact.MIME, Name: artifact.Name,
			DownloadURL: DoneBotMediaArtifactURL(task.TaskID, artifact.ID)})
	}
	return artifacts
}

func (task *DoneBotDispatch) IntentJob(kind, operation string) DoneBotMediaIntentJob {
	status := "running"
	switch task.State {
	case "queued":
		status = "queued"
	case "completed":
		status = "completed"
	case "failed":
		status = "failed"
	case "uncertain", "dispatching", "refunding":
		status = "uncertain"
	}
	currentAttempt := len(task.Attempts)
	if task.AccountID > 0 && (task.State == "dispatching" || task.State == "polling" || task.State == "retrieving" || task.State == "settling" || task.State == "uncertain") {
		currentAttempt++
	}
	reason := "processing"
	if status == "completed" {
		reason = "completed"
	} else if status == "failed" {
		reason = "all_models_failed"
	} else if task.State == "refunding" {
		reason = "refund_pending"
	} else if status == "uncertain" {
		reason = "dispatch_unconfirmed"
	}
	job := DoneBotMediaIntentJob{
		JobID: task.TaskID, TaskID: task.TaskID, ReceiptID: task.ReceiptID, Kind: kind, Operation: operation, Status: status,
		CurrentAttempt: currentAttempt, RetryEligibility: DoneBotMediaRetryEligibility{Eligible: false, State: "blocked", Reason: reason},
		Artifacts: []DoneBotMediaIntentArtifact{},
	}
	if task.State == "completed" {
		job.Artifacts = task.publicArtifacts()
	}
	if status == "failed" {
		job.Diagnostic = DoneBotMediaErrorDiagnostic(http.StatusBadGateway, "", []byte("All compatible media routes failed"))
	}
	if task.State == "refunding" {
		job.Diagnostic = nil
	}
	if status == "failed" || status == "completed" {
		job.Attempts = make([]DoneBotMediaIntentAttempt, 0, len(task.Attempts))
		for index, attempt := range task.Attempts {
			job.Attempts = append(job.Attempts, DoneBotMediaIntentAttempt{Attempt: index + 1, ReceiptID: task.ReceiptID, Status: attempt.Status})
		}
	}
	return job
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
	task.Attempts = append(task.Attempts, DoneBotDispatchAttempt{PublicModel: task.Model, AccountID: accountID, UpstreamModel: task.UpstreamModel, ProviderTaskID: task.ProviderTaskID, Status: "failed", Diagnostic: diagnostic})
	task.FailedAccounts = append(task.FailedAccounts, accountID)
	task.ProviderTaskID = ""
	task.State = "queued"
	task.Diagnostic = diagnostic
}

func (task *DoneBotDispatch) activateCandidate(index int) error {
	if task == nil || index < 0 || index >= len(task.Candidates) {
		return ErrDoneBotMediaInvalid
	}
	candidate := task.Candidates[index]
	if !safeMediaModel(candidate.PublicModel) || candidate.PublicModel == "" || !safeMediaModel(candidate.RoutingModel) || candidate.RoutingModel == "" ||
		!safeMediaModel(candidate.ForwardModel) || candidate.ForwardModel == "" || !safeMediaModel(candidate.BillingModel) || candidate.BillingModel == "" ||
		len(candidate.Body) == 0 || len(candidate.Body) > DoneBotMediaIntentBodyLimit || candidate.ContentType != "application/json" ||
		!strings.Contains(" image video audio ", " "+candidate.Kind+" ") {
		return ErrDoneBotMediaInvalid
	}
	switch candidate.Protocol {
	case DoneBotDispatchImages:
		if candidate.Path != "/v1/images/generations/async" && candidate.Path != "/v1/images/edits/async" {
			return ErrDoneBotMediaInvalid
		}
	case DoneBotDispatchAPIMartImages:
		if candidate.Kind != "image" || candidate.Path != "/v1/images/generations" || candidate.Info.Model != candidate.PublicModel {
			return ErrDoneBotMediaInvalid
		}
	case DoneBotDispatchAPIMartMusic:
		if candidate.Kind != "audio" || candidate.Path != "/v1/music/generations" || candidate.Info.Model != candidate.PublicModel {
			return ErrDoneBotMediaInvalid
		}
	case DoneBotDispatchSeedance:
		if candidate.Kind != "video" || candidate.Path != "/v1/videos" || candidate.VideoInfo.Model != candidate.PublicModel {
			return ErrDoneBotMediaInvalid
		}
	default:
		return ErrDoneBotMediaInvalid
	}
	for i := 0; i < index; i++ {
		if task.Candidates[i].PublicModel == candidate.PublicModel {
			return ErrDoneBotMediaInvalid
		}
	}
	task.CandidateIndex = index
	task.Model, task.BillingModel = candidate.PublicModel, candidate.BillingModel
	task.RoutingModel, task.ForwardModel = candidate.RoutingModel, candidate.ForwardModel
	task.Protocol, task.Path, task.ContentType = candidate.Protocol, candidate.Path, candidate.ContentType
	task.Body, task.Info, task.VideoInfo = append([]byte(nil), candidate.Body...), candidate.Info, candidate.VideoInfo
	task.References = append([]DoneBotMediaIntentReference(nil), candidate.References...)
	task.Kind = doneBotDispatchPrepaidKind(candidate.Protocol)
	return nil
}

func (task *DoneBotDispatch) advanceCandidate() bool {
	if task == nil || task.CandidateIndex+1 >= len(task.Candidates) {
		return false
	}
	if err := task.activateCandidate(task.CandidateIndex + 1); err != nil {
		task.Diagnostic = DoneBotMediaErrorDiagnostic(http.StatusBadRequest, "", []byte(err.Error()))
		task.State = "failed"
		return false
	}
	task.FailedAccounts = nil
	task.AccountID = 0
	task.UpstreamModel = ""
	task.ProviderTaskID = ""
	task.Result = nil
	task.Response = nil
	task.Diagnostic = nil
	task.State = "queued"
	return true
}

func doneBotDispatchPrepaidKind(protocol string) string {
	switch protocol {
	case DoneBotDispatchImages, DoneBotDispatchAPIMartImages:
		return "image"
	case DoneBotDispatchAPIMartMusic:
		return "music"
	case DoneBotDispatchSeedance:
		return "video"
	default:
		return ""
	}
}

func (task *DoneBotDispatch) validate() error {
	if task.ReceiptID == "" || task.TaskID == "" || task.UserID <= 0 || task.APIKeyID <= 0 || task.GroupID <= 0 || len(task.Body) == 0 {
		return ErrDoneBotMediaInvalid
	}
	if _, err := uuid.Parse(task.ReceiptID); err != nil {
		return ErrDoneBotMediaInvalid
	}
	if task.DoneBotIntent {
		if !doneBotMediaIntentOperation(task.IntentKind, task.IntentOperation) || len(task.Candidates) == 0 {
			return ErrDoneBotMediaInvalid
		}
		for _, candidate := range task.Candidates {
			if candidate.Kind != task.IntentKind {
				return ErrDoneBotMediaInvalid
			}
		}
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
