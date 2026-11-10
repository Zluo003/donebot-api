package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func (w *apimartSettlementWorker) dispatchDoneBot(ctx context.Context) {
	repo := w.service.doneBotDispatchRepository()
	if repo == nil {
		return
	}
	jobs, err := repo.TakeDoneBotDispatches(ctx, 4)
	if err != nil {
		logger.LegacyPrintf("service.donebot", "dispatch queue unavailable: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go func(task DoneBotDispatch) {
			defer wg.Done()
			attempt, cancel := context.WithTimeout(ctx, 30*time.Minute)
			defer cancel()
			if renewer, ok := repo.(interface {
				RenewDoneBotDispatchLease(context.Context, string, string) error
			}); ok {
				stopped := make(chan struct{})
				defer close(stopped)
				go func() {
					ticker := time.NewTicker(20 * time.Second)
					defer ticker.Stop()
					for {
						select {
						case <-stopped:
							return
						case <-attempt.Done():
							return
						case <-ticker.C:
							if err := renewer.RenewDoneBotDispatchLease(attempt, task.ReceiptID, task.Lease); err != nil {
								cancel()
								return
							}
						}
					}
				}()
			}
			if err := w.processDoneBot(attempt, &task); err != nil {
				logger.LegacyPrintf("service.donebot", "task %s recovery pending: %v", task.TaskID, err)
			}
		}(job)
	}
	wg.Wait()
}

func (w *apimartSettlementWorker) processDoneBot(ctx context.Context, task *DoneBotDispatch) error {
	s := w.service
	repo := s.doneBotDispatchRepository()
	ctx = WithDoneBotDispatch(WithDoneBotPriority(WithDoneBotMediaPrepaidSettlement(WithDoneBotMediaReceipt(ctx, task.ReceiptID))), task)
	ctx = WithOpenAIProfitControlSuppressed(WithResolvedTargetPlatform(ctx, PlatformOpenAI))
	ctx = WithAssetOwnerKeyID(ctx, task.APIKeyID)
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, task.ReceiptID)
	ctx = context.WithValue(ctx, mediaSettlementContextKey{}, true)
	ctx = WithCompositeRouteDecision(ctx, CompositeRouteDecision{Matched: true, TargetPlatform: PlatformOpenAI, UpstreamModel: task.RoutingModel, PublicModel: task.Model, Source: task.RouteSource})
	save := func(delay time.Duration) error {
		if task.Result != nil {
			task.Result.ResponseHeaders = nil
			task.Result.UpstreamHeaders = nil
		}
		return repo.SaveDoneBotDispatch(ctx, task, delay)
	}
	if task.State == "dispatching" {
		task.State = "uncertain"
		return save(0)
	}
	key, err := w.keys.apiKeyRepo.GetByID(ctx, task.APIKeyID)
	if err != nil {
		return err
	}
	if key == nil || key.UserID != task.UserID || key.User == nil {
		return fmt.Errorf("dispatch owner is unavailable")
	}
	if key.Group == nil || key.Group.ID != task.GroupID {
		group, groupErr := w.keys.groupRepo.GetByID(ctx, task.GroupID)
		if groupErr != nil {
			return groupErr
		}
		copyKey := *key
		copyKey.Group = group
		copyKey.GroupID = &task.GroupID
		key = &copyKey
	}
	if task.State == "queued" {
		excluded := map[int64]struct{}{}
		for _, id := range task.FailedAccounts {
			excluded[id] = struct{}{}
		}
		capability, imageCapability, capabilityErr := doneBotDispatchRequiredCapabilities(ctx, s, task)
		if capabilityErr != nil {
			return capabilityErr
		}
		selection, _, selectErr := s.selectAccountWithScheduler(ctx, &task.GroupID, "", "", task.RoutingModel, excluded, OpenAIUpstreamTransportHTTPSSE, capability, imageCapability, false, PlatformOpenAI, false, false)
		if selectErr != nil {
			if !errors.Is(selectErr, ErrNoAvailableAccounts) {
				return selectErr
			}
			exhausted, exhaustionErr := s.doneBotDispatchCandidateExhausted(ctx, task, capability, imageCapability)
			if exhaustionErr != nil {
				_ = save(10 * time.Second)
				return exhaustionErr
			}
			if !exhausted {
				return save(10 * time.Second)
			}
			if task.advanceCandidate() {
				return save(0)
			}
			task.State = "refunding"
			if err = save(0); err != nil {
				return err
			}
		} else if selection == nil || selection.Account == nil {
			exhausted, exhaustionErr := s.doneBotDispatchCandidateExhausted(ctx, task, capability, imageCapability)
			if exhaustionErr != nil {
				_ = save(10 * time.Second)
				return exhaustionErr
			}
			if !exhausted {
				return save(10 * time.Second)
			}
			if task.advanceCandidate() {
				return save(0)
			}
			task.State = "refunding"
			if err = save(0); err != nil {
				return err
			}
		} else {
			if !selection.Acquired {
				return save(5 * time.Second)
			}
			defer selection.ReleaseFunc()
			account := selection.Account
			if task.Protocol == DoneBotDispatchSeedance && !account.SeedanceAllowsResolution(task.RoutingModel, task.VideoInfo.Resolution) {
				task.failed(account.ID, nil)
				return save(time.Second)
			}
			requestBody, referenceErr := s.resolveDoneBotDispatchReferences(ctx, task)
			if referenceErr != nil {
				task.Diagnostic = DoneBotMediaErrorDiagnostic(http.StatusBadRequest, "", []byte(ErrAssetNotFound.Error()))
				task.State = "refunding"
				if err = save(0); err != nil {
					return err
				}
			} else {
				task.AccountID = account.ID
				task.UpstreamModel = account.GetMappedModel(task.ForwardModel)
				task.State = "dispatching"
				if err = save(90 * time.Second); err != nil {
					return err
				}
				if err = repo.ReassignDoneBotMediaAccount(ctx, task.ReceiptID, task.APIKeyID, account.ID, account.BillingRateMultiplier()); err != nil {
					task.State = "queued"
					if saveErr := save(time.Second); saveErr != nil {
						return saveErr
					}
					if errors.Is(err, ErrDoneBotMediaDispatchSpacing) {
						return nil
					}
					return err
				}
				if err = w.sendDoneBot(ctx, task, account, requestBody); err != nil {
					return err
				}
				if task.State == "queued" {
					return save(time.Second)
				}
				if !task.DoneBotIntent || task.State != "retrieving" {
					if err = save(90 * time.Second); err != nil {
						return err
					}
				}
			}
		}
	}
	if task.State == "polling" {
		account, accountErr := s.accountRepo.GetByID(ctx, task.AccountID)
		if accountErr != nil {
			return accountErr
		}
		if err = w.pollDoneBot(ctx, task, account); err != nil {
			_ = save(10 * time.Second)
			return err
		}
		if task.State == "queued" {
			return save(time.Second)
		}
		if !task.DoneBotIntent || task.State != "retrieving" {
			if err = save(90 * time.Second); err != nil {
				return err
			}
		}
	}
	if task.State == "retrieving" {
		if task.DoneBotIntent && len(task.Artifacts) > 0 && len(task.Response) == 0 {
			task.State = "settling"
		} else if task.Result == nil || len(task.Response) == 0 {
			return fmt.Errorf("completed task is missing durable output")
		} else {
			provider := "apimart"
			if task.Protocol == DoneBotDispatchSeedance {
				provider = "seedance"
			}
			if task.DoneBotIntent {
				kind := task.Kind
				if len(task.Candidates) > 0 && task.CandidateIndex >= 0 && task.CandidateIndex < len(task.Candidates) {
					kind = task.Candidates[task.CandidateIndex].Kind
				}
				rewritten, artifacts, artifactErr := s.PersistDoneBotDispatchArtifacts(ctx, task.APIKeyID, task.TaskID, provider, kind, task.Response)
				if artifactErr != nil || len(artifacts) == 0 {
					if artifactErr == nil {
						artifactErr = fmt.Errorf("completed media response has no persistable artifacts")
					}
					task.Response = nil
					task.Result = nil
					if task.ProviderTaskID != "" {
						task.State, task.Response, task.Result = "polling", nil, nil
						_ = save(10 * time.Second)
					} else {
						task.Diagnostic = DoneBotMediaErrorDiagnostic(http.StatusBadGateway, "", []byte(artifactErr.Error()))
						task.State = "refunding"
						if saveErr := save(0); saveErr != nil {
							return saveErr
						}
					}
					return artifactErr
				}
				task.Artifacts = artifacts
				if task.Protocol == DoneBotDispatchSeedance && task.Result != nil {
					task.Result.SeedanceResponseBody = rewritten
				}
				task.Response = nil
				if task.Result != nil {
					task.Result.SeedanceResponseBody = nil
				}
			} else if task.Protocol == DoneBotDispatchImages {
				if s.generatedMedia == nil {
					return fmt.Errorf("image storage unavailable")
				}
				storage, ok := s.generatedMedia.assets.(ImageStorage)
				if !ok {
					return fmt.Errorf("image storage does not retain results")
				}
				uploaded, uploadErr := NewImageResultUploader(storage, "", 0, nil).Rewrite(ctx, task.TaskID, json.RawMessage(task.Response))
				if uploadErr != nil {
					_ = save(10 * time.Second)
					return uploadErr
				}
				task.Response = uploaded
			} else {
				uploaded, uploadErr := s.RewriteGeneratedMedia(ctx, task.APIKeyID, task.TaskID, provider, task.Response)
				if uploadErr != nil {
					_ = save(10 * time.Second)
					return uploadErr
				}
				task.Response = uploaded
			}
			task.State = "settling"
			if err = save(90 * time.Second); err != nil {
				return err
			}
		}
	}
	if task.State == "settling" {
		account, accountErr := s.accountRepo.GetByID(ctx, task.AccountID)
		if accountErr != nil {
			return accountErr
		}
		if task.DoneBotIntent && task.Result == nil && len(task.Artifacts) > 0 {
			task.Result = &OpenAIForwardResult{Model: task.Model, BillingModel: task.BillingModel}
		}
		task.Result.ResponseID = task.TaskID
		task.Result.RequestID = "dbdispatch:" + task.ReceiptID
		task.Result.Model, task.Result.BillingModel = task.Model, task.BillingModel
		_, err = s.recordDoneBotPrepaidUsage(ctx, &OpenAIRecordUsageInput{Result: task.Result, APIKey: key, User: key.User, Account: account, APIKeyService: w.keys, QuotaPlatform: PlatformOpenAI})
		if err != nil {
			_ = save(10 * time.Second)
			return err
		}
		task.State = "completed"
		task.Attempts = append(task.Attempts, DoneBotDispatchAttempt{PublicModel: task.Model, AccountID: task.AccountID, UpstreamModel: task.UpstreamModel, ProviderTaskID: task.ProviderTaskID, Status: "completed"})
		task.Body = nil
		task.Result = nil
		task.Diagnostic = nil
		return save(0)
	}
	if task.State == "refunding" {
		if err = s.RefundDoneBotMediaReceipt(ctx); err != nil {
			_ = save(10 * time.Second)
			return err
		}
		task.State = "failed"
		task.Body = nil
		return save(0)
	}
	if task.State == "polling" {
		return save(5 * time.Second)
	}
	return nil
}

func doneBotDispatchRequiredCapabilities(ctx context.Context, service *OpenAIGatewayService, task *DoneBotDispatch) (OpenAIEndpointCapability, OpenAIImagesCapability, error) {
	capability := OpenAIEndpointCapability("")
	imageCapability := OpenAIImagesCapability("")
	switch task.Protocol {
	case DoneBotDispatchImages:
		c, _ := doneBotDispatchContext(ctx, task)
		parsed, err := service.ParseOpenAIImagesRequest(c, task.Body)
		if err != nil {
			return "", "", err
		}
		imageCapability = parsed.RequiredCapabilityForModel(task.ForwardModel)
	case DoneBotDispatchAPIMartImages:
		capability = OpenAIEndpointCapabilityAPIMartImages
	case DoneBotDispatchAPIMartMusic:
		capability = OpenAIEndpointCapabilityAPIMartSuno
	case DoneBotDispatchSeedance:
		capability = OpenAIEndpointCapabilitySeedance
	default:
		return "", "", ErrDoneBotMediaInvalid
	}
	return capability, imageCapability, nil
}

func (s *OpenAIGatewayService) doneBotDispatchCandidateExhausted(ctx context.Context, task *DoneBotDispatch, capability OpenAIEndpointCapability, imageCapability OpenAIImagesCapability) (bool, error) {
	if s == nil || s.accountRepo == nil || task == nil {
		return false, ErrDoneBotMediaPrepaidUnavailable
	}
	groupID := &task.GroupID
	includeGrouped := false
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		groupID = nil
		includeGrouped = true
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, groupID, []string{PlatformOpenAI}, includeGrouped)
	if err != nil {
		return false, err
	}
	failed := make(map[int64]struct{}, len(task.FailedAccounts))
	for _, id := range task.FailedAccounts {
		failed[id] = struct{}{}
	}
	for i := range accounts {
		account := &accounts[i]
		if NormalizeOpenAICompatiblePlatform(account.Platform) != PlatformOpenAI || !account.IsOpenAICompatible() ||
			!account.IsModelSupported(task.RoutingModel) || !accountSupportsOpenAICapabilities(account, capability, imageCapability) {
			continue
		}
		if task.Protocol == DoneBotDispatchSeedance && !account.SeedanceAllowsResolution(task.RoutingModel, task.VideoInfo.Resolution) {
			continue
		}
		if _, hasFailed := failed[account.ID]; !hasFailed {
			return false, nil
		}
	}
	return true, nil
}

func doneBotDispatchContext(ctx context.Context, task *DoneBotDispatch) (*gin.Context, *httptest.ResponseRecorder) {
	return doneBotDispatchContextWithBody(ctx, task, task.Body)
}

func doneBotDispatchContextWithBody(ctx context.Context, task *DoneBotDispatch, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "http://gateway.invalid"+task.Path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", task.ContentType)
	return c, recorder
}

func (w *apimartSettlementWorker) sendDoneBot(ctx context.Context, task *DoneBotDispatch, account *Account, requestBody []byte) error {
	s := w.service
	c, recorder := doneBotDispatchContextWithBody(ctx, task, requestBody)
	var err error
	var status int
	var body []byte
	switch task.Protocol {
	case DoneBotDispatchImages:
		var parsed *OpenAIImagesRequest
		parsed, err = s.ParseOpenAIImagesRequest(c, requestBody)
		if err == nil {
			err = parsed.ApplyDoneBotImagePolicy(task.Model)
		}
		if err == nil {
			task.Result, err = s.ForwardImages(ctx, c, account, requestBody, parsed, task.ForwardModel)
		}
		status, body = recorder.Code, recorder.Body.Bytes()
		var failover *UpstreamFailoverError
		if errors.As(err, &failover) {
			status, body = failover.StatusCode, failover.ResponseBody
		}
		if err == nil && task.Result != nil && task.Result.ImageCount > 0 {
			task.Response = bytes.Clone(body)
			task.State = "retrieving"
			return nil
		}
	case DoneBotDispatchSeedance:
		task.Result, err = s.ForwardSeedanceForBilling(ctx, c, account, SeedanceEndpointCreate, "", requestBody)
		status, body = recorder.Code, recorder.Body.Bytes()
		if err == nil && task.Result != nil && task.Result.ResponseID != "" {
			task.ProviderTaskID = strings.TrimPrefix(task.Result.ResponseID, "seedance:")
			task.State = "polling"
			return nil
		}
	case DoneBotDispatchAPIMartImages, DoneBotDispatchAPIMartMusic:
		endpoint := APIMartImages
		if task.Protocol == DoneBotDispatchAPIMartMusic {
			endpoint = APIMartSuno
		}
		var response *APIMartResponse
		response, err = s.ForwardAPIMart(ctx, c, account, endpoint, "", requestBody)
		if response != nil {
			status, body = response.Status, response.Body
			if err == nil && !response.IsFailure() && response.TaskID != "" {
				task.ProviderTaskID = response.TaskID
				task.UpstreamModel = response.Result.UpstreamModel
				task.State = "polling"
				return nil
			}
		}
	}
	task.Diagnostic = DoneBotMediaErrorDiagnostic(status, recorder.Header().Get("X-Request-Id"), body)
	if DoneBotExplicitMediaRejection(status, body) {
		s.ReportOpenAIAccountScheduleResult(account, task.UpstreamModel, false, nil)
		task.failed(account.ID, task.Diagnostic)
	} else {
		task.State = "uncertain"
	}
	return nil
}

func (w *apimartSettlementWorker) pollDoneBot(ctx context.Context, task *DoneBotDispatch, account *Account) error {
	c, _ := doneBotDispatchContext(ctx, task)
	var response []byte
	if task.Protocol == DoneBotDispatchSeedance {
		result, err := w.service.ForwardSeedanceForBilling(ctx, c, account, SeedanceEndpointStatus, SeedanceTaskKey(task.ProviderTaskID), nil)
		if err != nil {
			return err
		}
		response = result.SeedanceResponseBody
		pending := &GrokVideoPendingBilling{Model: task.Model, BillingModel: task.BillingModel, OriginalModel: task.Model, UpstreamModel: task.UpstreamModel, VideoResolution: task.VideoInfo.Resolution, VideoInputDurationSeconds: task.VideoInfo.ReferenceVideoDurationSeconds, VideoReference: task.VideoInfo.HasReferenceVideo, CreatedAt: task.CreatedAt.Format(time.RFC3339Nano)}
		task.Result = MergeSeedanceCompletion(result, pending, task.TaskID)
	} else {
		endpoint := APIMartImageTask
		create := APIMartImages
		if task.Protocol == DoneBotDispatchAPIMartMusic {
			endpoint = APIMartMusicTask
			create = APIMartSuno
		}
		result, err := w.service.ForwardAPIMart(ctx, c, account, endpoint, task.ProviderTaskID, nil)
		if err != nil {
			return err
		}
		nativeStatus := gjson.GetBytes(result.Body, "data.status").String()
		if nativeStatus == "" {
			nativeStatus = gjson.GetBytes(result.Body, "status").String()
		}
		if result.IsFailure() && nativeStatus != "failed" && nativeStatus != "error" && nativeStatus != "cancelled" && nativeStatus != "expired" {
			return fmt.Errorf("upstream task query status %d", result.Status)
		}
		response = result.Body
		pending := &APIMartPending{Endpoint: create, Model: task.Model, BillingModel: task.BillingModel, OriginalModel: task.Model, UpstreamModel: task.UpstreamModel, Tier: task.Info.Tier, Units: task.Info.Units, LayerDecomposition: task.Info.LayerDecomposition, CreatedAt: task.CreatedAt.Format(time.RFC3339Nano)}
		task.Result = APIMartCompletionResult(pending, response, task.TaskID)
	}
	if task.Result != nil {
		task.Response = bytes.Clone(response)
		task.State = "retrieving"
		return nil
	}
	root := gjson.ParseBytes(response)
	if root.Get("data").IsObject() {
		root = root.Get("data")
	}
	switch strings.ToLower(root.Get("status").String()) {
	case "failed", "error", "cancelled", "canceled", "expired":
		diagnostic := DoneBotMediaErrorDiagnostic(http.StatusBadRequest, "", response)
		task.failed(account.ID, diagnostic)
	}
	return nil
}
