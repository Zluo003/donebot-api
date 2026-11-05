//go:build unit

package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type doneBotMediaRecoveryStub struct {
	owner         service.DoneBotMediaOwner
	resolveCalls  int
	begin         []service.DoneBotMediaBeginInput
	validateCalls int
	validateErr   error
	completion    []service.DoneBotMediaCompletion
	observed      []doneBotMediaObservation
	operations    map[string]*service.DoneBotMediaOperation
	resolveErr    error
	completeErr   error
	observeErr    error
}

type doneBotMediaObservation struct {
	owner        service.DoneBotMediaOwner
	taskID, kind string
	status       string
}

func newDoneBotMediaRecoveryStub() *doneBotMediaRecoveryStub {
	return &doneBotMediaRecoveryStub{owner: service.DoneBotMediaOwner{UserID: 10, APIKeyID: 20, GroupID: 24, InstallationID: "installation"}, operations: map[string]*service.DoneBotMediaOperation{}}
}

func (s *doneBotMediaRecoveryStub) ResolveMediaOwner(_ context.Context, userID, keyID, groupID int64) (service.DoneBotMediaOwner, error) {
	s.resolveCalls++
	if userID != s.owner.UserID || keyID != s.owner.APIKeyID || groupID != s.owner.GroupID {
		return service.DoneBotMediaOwner{}, service.ErrDoneBotMediaNotFound
	}
	return s.owner, s.resolveErr
}

func (s *doneBotMediaRecoveryStub) BeginMedia(_ context.Context, in service.DoneBotMediaBeginInput) (*service.DoneBotMediaBeginResult, error) {
	s.begin = append(s.begin, in)
	if in.IdempotencyKey == "" {
		return nil, service.ErrDoneBotMediaInvalid
	}
	if op := s.operations[in.IdempotencyKey]; op != nil {
		result := &service.DoneBotMediaBeginResult{Operation: op}
		if op.Fingerprint != in.Fingerprint {
			return result, service.ErrDoneBotMediaConflict
		}
		if len(op.ReplayBody) == 0 || op.Status == "uncertain" {
			return result, service.ErrDoneBotMediaPending
		}
		result.Replay = true
		return result, nil
	}
	op := &service.DoneBotMediaOperation{ReceiptID: uuid.NewString(), Owner: in.Owner, Fingerprint: in.Fingerprint, Kind: in.Kind, Model: in.Model, Status: "dispatching"}
	s.operations[in.IdempotencyKey] = op
	return &service.DoneBotMediaBeginResult{Operation: op}, nil
}

func (s *doneBotMediaRecoveryStub) CompleteMedia(_ context.Context, owner service.DoneBotMediaOwner, receiptID string, completion service.DoneBotMediaCompletion) (*service.DoneBotMediaOperation, error) {
	s.completion = append(s.completion, completion)
	for _, op := range s.operations {
		if op.ReceiptID != receiptID || op.Owner != owner {
			continue
		}
		op.Status, op.TaskID, op.TaskKey, op.Provider, op.UsageRequestID = completion.Status, completion.TaskID, completion.TaskKey, completion.Provider, completion.UsageRequestID
		op.ReplayBody, op.ReplayContentType, op.ReplayStatus, op.ReplayRetryAfter = bytes.Clone(completion.Body), completion.ContentType, completion.HTTPStatus, completion.RetryAfter
		return op, s.completeErr
	}
	return nil, service.ErrDoneBotMediaNotFound
}

func (s *doneBotMediaRecoveryStub) ObserveMedia(_ context.Context, owner service.DoneBotMediaOwner, taskID, kind, status string) (*service.DoneBotMediaOperation, error) {
	s.observed = append(s.observed, doneBotMediaObservation{owner: owner, taskID: taskID, kind: kind, status: status})
	return nil, s.observeErr
}

func (s *doneBotMediaRecoveryStub) ValidateMedia(_ context.Context, _ service.DoneBotMediaOwner, _, _ string, _ map[string]any) error {
	s.validateCalls++
	return s.validateErr
}

func doneBotMediaTestRouter(t *testing.T, recovery DoneBotMediaRecovery, doneBot bool, handler gin.HandlerFunc, before ...gin.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(24)
		key := "regular-api-key"
		if doneBot {
			key = "sk-donebot-private"
		}
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 20, UserID: 10, GroupID: &groupID, Key: key})
		c.Next()
	})
	router.Use(before...)
	router.Use(DoneBotMediaRecoveryMiddleware(recovery))
	router.Any("/*path", handler)
	return router
}

func doneBotMediaTestRequest(router http.Handler, method, path, contentType, key string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestDoneBotMediaRecoverySubmitsOnceAndReplaysNativeResponse(t *testing.T) {
	recovery := newDoneBotMediaRecoveryStub()
	generated := 0
	native := `{"code":200,"data":[{"status":"submitted","task_id":"image-task"}]}`
	body := []byte(`{"model":"image-model","prompt":"waves","size":"2K"}`)
	router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) {
		generated++
		actual, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, body, actual)
		require.Equal(t, c.Writer.Header().Get(clientRequestIDHeader), c.Request.Context().Value(ctxkey.ClientRequestID))
		c.Header("Retry-After", "3")
		c.Data(http.StatusAccepted, "application/json", []byte(native))
	})
	first := doneBotMediaTestRequest(router, http.MethodPost, "/v1/images/generations", "application/json", "request-1", body)
	second := doneBotMediaTestRequest(router, http.MethodPost, "/v1/images/generations", "application/json", "request-1", []byte(`{ "size":"2K", "prompt":"waves", "model":"image-model" }`))
	require.Equal(t, 1, generated)
	require.Len(t, recovery.begin, 2)
	require.Len(t, recovery.completion, 1)
	require.Equal(t, 1, recovery.validateCalls)
	require.Equal(t, recovery.owner, recovery.begin[0].Owner)
	require.Equal(t, "image-model", recovery.begin[0].Model)
	require.Equal(t, recovery.begin[0].Fingerprint, recovery.begin[1].Fingerprint)
	for _, w := range []*httptest.ResponseRecorder{first, second} {
		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
		require.Equal(t, native, w.Body.String())
		require.Equal(t, "application/json", w.Header().Get("Content-Type"))
		require.Equal(t, "3", w.Header().Get("Retry-After"))
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.NotEmpty(t, w.Header().Get("X-DoneBot-Receipt-ID"))
	}
	require.Equal(t, first.Header().Get("X-DoneBot-Receipt-ID"), second.Header().Get("X-DoneBot-Receipt-ID"))
	require.Equal(t, first.Header().Get(clientRequestIDHeader), second.Header().Get(clientRequestIDHeader))
	require.Equal(t, "true", second.Header().Get("Idempotency-Replayed"))
	require.Equal(t, "accepted", recovery.completion[0].Status)
	require.Equal(t, "apimart:image:image-task", recovery.completion[0].UsageRequestID)
	conflict := doneBotMediaTestRequest(router, http.MethodPost, "/v1/images/generations", "application/json", "request-1", []byte(`{"model":"image-model","prompt":"different waves","size":"2K"}`))
	require.Equal(t, http.StatusConflict, conflict.Code)
	require.Equal(t, 1, generated)
	require.Len(t, recovery.completion, 1)
}

func TestDoneBotMediaRecoveryRequiresIdempotencyAndDoesNotRetryUnknown(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			recovery := newDoneBotMediaRecoveryStub()
			generated := 0
			router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) {
				generated++
				c.Data(status, "application/json", []byte(`{"error":{"type":"upstream_error","message":"unknown provider outcome"}}`))
			})
			body := []byte(`{"model":"video-model","content":[{"type":"text","text":"waves"}]}`)
			missing := doneBotMediaTestRequest(router, http.MethodPost, "/v1/videos", "application/json", "", body)
			require.Equal(t, http.StatusBadRequest, missing.Code)
			require.Zero(t, generated)
			first := doneBotMediaTestRequest(router, http.MethodPost, "/v1/videos", "application/json", "uncertain", body)
			second := doneBotMediaTestRequest(router, http.MethodPost, "/v1/videos", "application/json", "uncertain", body)
			require.Equal(t, status, first.Code)
			require.Equal(t, http.StatusConflict, second.Code)
			require.Equal(t, 1, generated)
			require.Len(t, recovery.completion, 1)
			require.Equal(t, "uncertain", recovery.completion[0].Status)
			require.Equal(t, first.Header().Get("X-DoneBot-Receipt-ID"), second.Header().Get("X-DoneBot-Receipt-ID"))
		})
	}
}

func TestDoneBotMediaRecoveryFailsOnlyConfirmedUndispatched503(t *testing.T) {
	for _, tc := range []struct {
		name       string
		flag       any
		taskID     string
		usageID    string
		usageSet   bool
		httpStatus int
		wantStatus string
	}{
		{name: "confirmed undispatched", flag: true, wantStatus: "failed"},
		{name: "unconfirmed same error message", wantStatus: "uncertain"},
		{name: "false marker", flag: false, wantStatus: "uncertain"},
		{name: "nonboolean marker", flag: "true", wantStatus: "uncertain"},
		{name: "task already recorded", flag: true, taskID: "accepted-task", wantStatus: "uncertain"},
		{name: "billing already recorded", flag: true, usageID: "accepted-usage", wantStatus: "uncertain"},
		{name: "empty billing marker", flag: true, usageSet: true, wantStatus: "uncertain"},
		{name: "other server failure", flag: true, httpStatus: http.StatusBadGateway, wantStatus: "uncertain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := tc.httpStatus
			if status == 0 {
				status = http.StatusServiceUnavailable
			}
			recovery := newDoneBotMediaRecoveryStub()
			dispatched := 0
			router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) {
				dispatched++
				if tc.flag != nil {
					c.Set(DoneBotMediaNotDispatched, tc.flag)
				}
				if tc.taskID != "" {
					c.Set(DoneBotMediaTaskID, tc.taskID)
				}
				if tc.usageSet || tc.usageID != "" {
					c.Set(DoneBotMediaUsageRequestID, tc.usageID)
				}
				c.Data(status, "application/json", []byte(`{"error":{"type":"api_error","message":"No eligible APIMart accounts"}}`))
			})
			body := []byte(`{"model":"image-model","prompt":"waves"}`)
			first := doneBotMediaTestRequest(router, http.MethodPost, "/v1/images/generations", "application/json", "not-dispatched", body)
			require.Equal(t, status, first.Code)
			require.Len(t, recovery.completion, 1)
			completion := recovery.completion[0]
			require.Equal(t, tc.wantStatus, completion.Status)
			require.Equal(t, tc.taskID, completion.TaskID)
			wantUsageID := tc.usageID
			if tc.taskID != "" && wantUsageID == "" {
				wantUsageID = "apimart:image:" + tc.taskID
			}
			require.Equal(t, wantUsageID, completion.UsageRequestID)
			second := doneBotMediaTestRequest(router, http.MethodPost, "/v1/images/generations", "application/json", "not-dispatched", body)
			if tc.wantStatus == "failed" {
				require.Equal(t, http.StatusServiceUnavailable, second.Code)
				require.Equal(t, "true", second.Header().Get("Idempotency-Replayed"))
			} else {
				require.Equal(t, http.StatusConflict, second.Code)
			}
			require.Equal(t, 1, dispatched)
		})
	}
}

func TestDoneBotMediaRecoveryKeepsRejectionStatusAndRetryAfter(t *testing.T) {
	for _, status := range []int{http.StatusPaymentRequired, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			recovery := newDoneBotMediaRecoveryStub()
			dispatched := 0
			native := fmt.Sprintf(`{"error":{"type":"native_%d","message":"provider rejection"}}`, status)
			router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) {
				dispatched++
				c.Header("Retry-After", "11")
				c.Data(status, "application/json", []byte(native))
			})
			body := []byte(`{"model":"image-model","prompt":"waves"}`)
			for i := 0; i < 2; i++ {
				w := doneBotMediaTestRequest(router, http.MethodPost, "/v1/images/generations", "application/json", "rejected", body)
				require.Equal(t, status, w.Code)
				require.Equal(t, native, w.Body.String())
				require.Equal(t, "11", w.Header().Get("Retry-After"))
			}
			require.Equal(t, 1, dispatched)
			require.Len(t, recovery.completion, 1)
			require.Equal(t, "failed", recovery.completion[0].Status)
		})
	}
	blocked := newDoneBotMediaRecoveryStub()
	router := doneBotMediaTestRouter(t, blocked, true, func(c *gin.Context) { t.Fatal("authentication rejected request reached provider") }, func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusPaymentRequired, gin.H{"error": "insufficient_balance"})
	})
	w := doneBotMediaTestRequest(router, http.MethodPost, "/v1/images/generations", "application/json", "rejected-before-begin", []byte(`{"model":"image-model"}`))
	require.Equal(t, http.StatusPaymentRequired, w.Code)
	require.Empty(t, blocked.begin)
	require.Zero(t, blocked.resolveCalls)
}

func TestDoneBotMediaRecoveryKeepsAcceptedTaskWhenPersistenceReturnsFailure(t *testing.T) {
	recovery := newDoneBotMediaRecoveryStub()
	dispatched := 0
	router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) {
		dispatched++
		c.Set(DoneBotMediaProvider, "seedance")
		c.Set(DoneBotMediaTaskID, "accepted-task")
		c.Set(DoneBotMediaUsageRequestID, service.StableGrokVideoBillingRequestID(service.SeedanceTaskKey("accepted-task")))
		c.Data(http.StatusServiceUnavailable, "application/json", []byte(`{"error":{"message":"Accepted task could not be queued; do not resubmit"}}`))
	})
	body := []byte(`{"model":"video-model","content":[{"type":"text","text":"waves"}]}`)
	w := doneBotMediaTestRequest(router, http.MethodPost, "/v1/videos", "application/json", "accepted", body)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Len(t, recovery.completion, 1)
	completion := recovery.completion[0]
	require.Equal(t, "uncertain", completion.Status)
	require.Equal(t, "accepted-task", completion.TaskID)
	require.Equal(t, service.SeedanceTaskKey("accepted-task"), completion.TaskKey)
	require.Equal(t, service.StableGrokVideoBillingRequestID(service.SeedanceTaskKey("accepted-task")), completion.UsageRequestID)
	w = doneBotMediaTestRequest(router, http.MethodPost, "/v1/videos", "application/json", "accepted", body)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Equal(t, 1, dispatched)
}

func TestDoneBotMediaRecoveryPollObservesNativeStatusWithoutChangingResponse(t *testing.T) {
	for _, tc := range []struct{ path, body, task, kind, status string }{
		{"/v1/tasks/image-task", `{"code":200,"data":{"status":"success","result":{"images":[{"url":"https://cdn.example/image.png"}]}}}`, "image-task", "image", "success"},
		{"/v1/music/tasks/music-task", `{"data":{"status":"completed","result":{"music":[{"audio_url":"https://cdn.example/audio.mp3"}]}}}`, "music-task", "music", "completed"},
		{"/v1/videos/video-task", `{"id":"video-task","status":"succeeded","content":{"video_url":"https://cdn.example/video.mp4"}}`, "video-task", "video", "succeeded"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			recovery := newDoneBotMediaRecoveryStub()
			recovery.observeErr = service.ErrDoneBotMediaNotFound
			router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) { c.Data(http.StatusOK, "application/json", []byte(tc.body)) })
			w := doneBotMediaTestRequest(router, http.MethodGet, tc.path, "", "", nil)
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, tc.body, w.Body.String())
			require.Equal(t, []doneBotMediaObservation{{owner: recovery.owner, taskID: tc.task, kind: tc.kind, status: tc.status}}, recovery.observed)
			require.Empty(t, recovery.begin)
			require.Empty(t, recovery.completion)
		})
	}
}

func TestDoneBotMediaRecoveryOverflowForwardsAllBytesWithoutReplayCache(t *testing.T) {
	recovery := newDoneBotMediaRecoveryStub()
	data := bytes.Repeat([]byte("voice"), service.DoneBotMediaReplayLimit/5+2)
	generated := 0
	router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) {
		generated++
		c.Header("Content-Type", "audio/wav")
		_, _ = c.Writer.Write(data[:100])
		_, _ = c.Writer.Write(data[100:])
	})
	body := []byte(`{"model":"speech-model","input":"hello"}`)
	w := doneBotMediaTestRequest(router, http.MethodPost, "/v1/audio/speech", "application/json", "large", body)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, data, w.Body.Bytes())
	require.Equal(t, "audio/wav", w.Header().Get("Content-Type"))
	require.Len(t, recovery.completion, 1)
	require.Nil(t, recovery.completion[0].Body)
	w = doneBotMediaTestRequest(router, http.MethodPost, "/v1/audio/speech", "application/json", "large", body)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Equal(t, 1, generated)
}

func TestDoneBotMediaRecoveryBypassesRegularKeysAndRejectsBeforeProvider(t *testing.T) {
	called := 0
	router := doneBotMediaTestRouter(t, nil, false, func(c *gin.Context) {
		called++
		c.String(http.StatusCreated, "regular")
	})
	w := doneBotMediaTestRequest(router, http.MethodPost, "/v1/videos", "", "", nil)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, 1, called)
	recovery := newDoneBotMediaRecoveryStub()
	recovery.validateErr = infraerrors.New(http.StatusForbidden, "CATALOG_SCOPE", "model outside installation scope")
	router = doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) { t.Fatal("validation failure reached provider") })
	w = doneBotMediaTestRequest(router, http.MethodPost, "/v1/videos", "application/json", "scope", []byte(`{"model":"private-video"}`))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Len(t, recovery.completion, 1)
	require.Equal(t, "failed", recovery.completion[0].Status)
}

func TestDoneBotMediaRecoveryRejectsLegacyCreationAliases(t *testing.T) {
	for _, path := range []string{
		"/v1/videos/generations", "/videos", "/videos/edits", "/images/generations",
		"/api/v3/contents/generations/tasks", "/v1/contents/generations/tasks", "/tts", "/v1/tts",
	} {
		t.Run(path, func(t *testing.T) {
			recovery := newDoneBotMediaRecoveryStub()
			router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) { t.Fatal("legacy media path reached provider") })
			w := doneBotMediaTestRequest(router, http.MethodPost, path, "application/json", "legacy", []byte(`{"model":"media-model"}`))
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "DONEBOT_MEDIA_NATIVE_PATH_REQUIRED")
			require.Empty(t, recovery.begin)
		})
	}
}

func TestDoneBotMediaRecoveryAsyncImagesReplayAndKeepReceiptMoneyIdentity(t *testing.T) {
	for _, path := range []string{"/v1/images/generations/async", "/v1/images/edits/async"} {
		t.Run(path, func(t *testing.T) {
			recovery := newDoneBotMediaRecoveryStub()
			calls := 0
			router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) {
				calls++
				receipt := c.Writer.Header().Get("X-DoneBot-Receipt-ID")
				task := service.DoneBotImageTaskID(receipt)
				c.Set(DoneBotMediaProvider, "donebot")
				c.Set(DoneBotMediaTaskID, task)
				c.JSON(202, gin.H{"id": task, "status": "processing", "receipt_id": receipt})
			})
			w := doneBotMediaTestRequest(router, "POST", path, "application/json", "same", []byte(`{"model":"gpt-image-1","prompt":"waves"}`))
			require.Equal(t, 202, w.Code, w.Body.String())
			receipt := w.Header().Get("X-DoneBot-Receipt-ID")
			require.Len(t, recovery.completion, 1)
			completion := recovery.completion[0]
			require.Equal(t, service.DoneBotImageTaskID(receipt), completion.TaskKey)
			require.Equal(t, "client:"+receipt, completion.UsageRequestID)
			w = doneBotMediaTestRequest(router, "POST", path, "application/json", "same", []byte(`{"prompt":"waves","model":"gpt-image-1"}`))
			require.Equal(t, 202, w.Code)
			require.Equal(t, "true", w.Header().Get("Idempotency-Replayed"))
			require.Equal(t, receipt, w.Header().Get("X-DoneBot-Receipt-ID"))
			require.Equal(t, 1, calls)
		})
	}
}

func TestDoneBotSpeechIncompleteStreamCannotReplayPartialAudio(t *testing.T) {
	recovery := newDoneBotMediaRecoveryStub()
	calls := 0
	router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) {
		calls++
		c.Set(DoneBotMediaProvider, "apimart")
		c.Set(DoneBotMediaUsageRequestID, "apimart:tts:provider-request")
		c.Set(DoneBotMediaStreamStatus, "uncertain")
		c.Set(DoneBotMediaStreamDelivered, false)
		c.Data(200, "audio/opus", []byte("partial"))
	})
	body := []byte(`{"model":"speech","input":"hello","voice":"alloy"}`)
	w := doneBotMediaTestRequest(router, "POST", "/v1/audio/speech", "application/json", "tts", body)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "uncertain", recovery.completion[0].Status)
	require.Empty(t, recovery.completion[0].Body)
	w = doneBotMediaTestRequest(router, "POST", "/v1/audio/speech", "application/json", "tts", body)
	require.Equal(t, 409, w.Code)
	require.Equal(t, 1, calls)
}

type doneBotMediaPathEstimatorStub struct {
	*doneBotMediaRecoveryStub
	events                  []string
	path                    string
	estimated               *json.Number
	validatePathErr, setErr error
}

func (s *doneBotMediaPathEstimatorStub) ValidateMediaPath(_ context.Context, _ service.DoneBotMediaOwner, _, _, path string, _ map[string]any) error {
	s.events = append(s.events, "validate path")
	s.path = path
	return s.validatePathErr
}

func (s *doneBotMediaPathEstimatorStub) EstimateMedia(context.Context, service.DoneBotMediaOwner, string, string, map[string]any) (*json.Number, error) {
	s.events = append(s.events, "estimate")
	value := json.Number("123.45")
	return &value, nil
}

func (s *doneBotMediaPathEstimatorStub) SetMediaEstimate(_ context.Context, _ service.DoneBotMediaOwner, _ string, estimate *json.Number) error {
	s.events = append(s.events, "persist estimate")
	s.estimated = estimate
	return s.setErr
}

func TestDoneBotMediaRecoveryValidatesOperationAndPersistsEstimateBeforeProvider(t *testing.T) {
	for _, failure := range []string{"", "path", "estimate persistence"} {
		t.Run(failure, func(t *testing.T) {
			recovery := &doneBotMediaPathEstimatorStub{doneBotMediaRecoveryStub: newDoneBotMediaRecoveryStub()}
			if failure == "path" {
				recovery.validatePathErr = service.ErrDoneBotMediaInvalid
			}
			if failure == "estimate persistence" {
				recovery.setErr = errors.New("estimate storage unavailable")
			}
			router := doneBotMediaTestRouter(t, recovery, true, func(c *gin.Context) {
				recovery.events = append(recovery.events, "provider")
				c.Data(http.StatusOK, "application/json", []byte(`{"id":"video-task"}`))
			})
			w := doneBotMediaTestRequest(router, http.MethodPost, "/v1/videos/edits", "application/json", "estimated", []byte(`{"model":"video-model","prompt":"waves"}`))
			require.Equal(t, "/v1/videos/edits", recovery.path)
			require.Zero(t, recovery.validateCalls)
			switch failure {
			case "path":
				require.Equal(t, http.StatusBadRequest, w.Code)
				require.Equal(t, []string{"validate path"}, recovery.events)
			case "estimate persistence":
				require.Equal(t, http.StatusInternalServerError, w.Code)
				require.Equal(t, []string{"validate path", "estimate", "persist estimate"}, recovery.events)
			default:
				require.Equal(t, http.StatusOK, w.Code)
				require.Equal(t, []string{"validate path", "estimate", "persist estimate", "provider"}, recovery.events)
				require.Equal(t, "123.45", recovery.estimated.String())
			}
			require.Len(t, recovery.completion, 1)
		})
	}
}

type doneBotMultipartPart struct{ name, filename, value string }

func doneBotTestMultipart(t *testing.T, boundary string, parts []doneBotMultipartPart) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.SetBoundary(boundary))
	for _, part := range parts {
		var writer io.Writer
		var err error
		if part.filename == "" {
			writer, err = w.CreateFormField(part.name)
		} else {
			writer, err = w.CreateFormFile(part.name, part.filename)
		}
		require.NoError(t, err)
		_, err = io.Copy(writer, strings.NewReader(part.value))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return body.Bytes(), w.FormDataContentType()
}

func TestDoneBotMediaRequestFingerprintCanonicalizesJSONAndMultipartBoundaries(t *testing.T) {
	_, first, err := doneBotMediaParameters(http.MethodPost, "/v1/videos", "application/json", []byte(`{"model":"m","duration":5,"content":[{"type":"text","text":"waves"}]}`))
	require.NoError(t, err)
	_, same, err := doneBotMediaParameters(http.MethodPost, "/v1/videos", "application/json; charset=utf-8", []byte(`{ "content":[{"text":"waves","type":"text"}],"duration":5,"model":"m" }`))
	require.NoError(t, err)
	require.Equal(t, first, same)
	_, changed, err := doneBotMediaParameters(http.MethodPost, "/v1/videos", "application/json", []byte(`{"model":"m","duration":6,"content":[{"type":"text","text":"waves"}]}`))
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
	_, first, err = doneBotMediaParameters(http.MethodPost, "/v1/videos", "application/json", []byte(`{"model":"m","seed":9007199254740992}`))
	require.NoError(t, err)
	_, changed, err = doneBotMediaParameters(http.MethodPost, "/v1/videos", "application/json", []byte(`{"model":"m","seed":9007199254740993}`))
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
	parts := []doneBotMultipartPart{{"model", "", "image-model"}, {"prompt", "", "waves"}, {"image", "reference.png", "image bytes"}}
	body, contentType := doneBotTestMultipart(t, "boundary-a", parts)
	parameters, first, err := doneBotMediaParameters(http.MethodPost, "/v1/images/edits", contentType, body)
	require.NoError(t, err)
	require.Equal(t, "image-model", parameters["model"])
	require.NotContains(t, parameters, "image")
	body, contentType = doneBotTestMultipart(t, "boundary-b", []doneBotMultipartPart{parts[2], parts[1], parts[0]})
	_, same, err = doneBotMediaParameters(http.MethodPost, "/v1/images/edits", contentType, body)
	require.NoError(t, err)
	require.Equal(t, first, same)
	parts[2].value = "different image bytes"
	body, contentType = doneBotTestMultipart(t, "boundary-c", parts)
	_, changed, err = doneBotMediaParameters(http.MethodPost, "/v1/images/edits", contentType, body)
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
	_, _, err = doneBotMediaParameters(http.MethodPost, "/v1/images/edits", "multipart/form-data", []byte("invalid"))
	require.Error(t, err)
}

func TestDoneBotMediaMultipartFingerprintPreservesRepeatedReferenceOrder(t *testing.T) {
	parts := []doneBotMultipartPart{{"model", "", "image-model"}, {"image", "reference.png", "first image"}, {"image", "reference.png", "second image"}}
	body, contentType := doneBotTestMultipart(t, "a", parts)
	_, first, err := doneBotMediaParameters(http.MethodPost, "/v1/images/edits", contentType, body)
	require.NoError(t, err)
	body, contentType = doneBotTestMultipart(t, "b", []doneBotMultipartPart{parts[0], parts[2], parts[1]})
	_, reordered, err := doneBotMediaParameters(http.MethodPost, "/v1/images/edits", contentType, body)
	require.NoError(t, err)
	require.NotEqual(t, first, reordered)
}
