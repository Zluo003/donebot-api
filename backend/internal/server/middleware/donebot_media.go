package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const DoneBotMediaUsageRequestID = "donebot.media.usage_request_id"
const DoneBotMediaProvider = "donebot.media.provider"
const DoneBotMediaTaskID = "donebot.media.task_id"
const DoneBotMediaStreamStatus = "donebot.media.stream_status"
const DoneBotMediaStreamDelivered = "donebot.media.stream_delivered"
const DoneBotMediaNotDispatched = "donebot.media.not_dispatched"

type DoneBotMediaRecovery interface {
	ResolveMediaOwner(context.Context, int64, int64, int64) (service.DoneBotMediaOwner, error)
	BeginMedia(context.Context, service.DoneBotMediaBeginInput) (*service.DoneBotMediaBeginResult, error)
	CompleteMedia(context.Context, service.DoneBotMediaOwner, string, service.DoneBotMediaCompletion) (*service.DoneBotMediaOperation, error)
	ObserveMedia(context.Context, service.DoneBotMediaOwner, string, string, string) (*service.DoneBotMediaOperation, error)
	ValidateMedia(context.Context, service.DoneBotMediaOwner, string, string, map[string]any) error
}

func DoneBotMediaRecoveryMiddleware(recovery DoneBotMediaRecovery) gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := GetAPIKeyFromContext(c)
		if !ok || key == nil || !strings.HasPrefix(key.Key, "sk-donebot-") {
			c.Next()
			return
		}
		if doneBotLegacyMediaCreate(c.Request.Method, c.Request.URL.Path) {
			response.ErrorFrom(c, infraerrors.BadRequest("DONEBOT_MEDIA_NATIVE_PATH_REQUIRED", "Use a native DoneBot media path under /v1"))
			c.Abort()
			return
		}
		kind, taskID := doneBotMediaRequest(c.Request.Method, c.Request.URL.Path)
		if kind == "" {
			c.Next()
			return
		}
		if recovery == nil || key.GroupID == nil {
			response.ErrorFrom(c, service.ErrDoneBotGroupUnavailable)
			c.Abort()
			return
		}
		owner, err := recovery.ResolveMediaOwner(c.Request.Context(), key.UserID, key.ID, *key.GroupID)
		if response.ErrorFrom(c, err) {
			c.Abort()
			return
		}
		if c.Request.Method == http.MethodGet {
			writer := &doneBotMediaCapture{ResponseWriter: c.Writer}
			c.Writer = writer
			c.Next()
			if writer.Status() >= 200 && writer.Status() < 300 && !writer.overflow {
				status := gjson.GetBytes(writer.body.Bytes(), "data.status").String()
				if status == "" {
					status = gjson.GetBytes(writer.body.Bytes(), "status").String()
				}
				if status != "" {
					ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
					if _, err := recovery.ObserveMedia(ctx, owner, taskID, kind, status); err != nil && !errors.Is(err, service.ErrDoneBotMediaNotFound) {
						_ = c.Error(err)
					}
					cancel()
				}
			}
			return
		}
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			status := http.StatusBadRequest
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			response.ErrorFrom(c, infraerrors.New(status, "DONEBOT_MEDIA_BODY_INVALID", "Unable to read media request"))
			c.Abort()
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		parameters, fingerprint, err := doneBotMediaParameters(c.Request.Method, c.Request.URL.Path, c.GetHeader("Content-Type"), body)
		if response.ErrorFrom(c, err) {
			c.Abort()
			return
		}
		model, _ := parameters["model"].(string)
		result, err := recovery.BeginMedia(c.Request.Context(), service.DoneBotMediaBeginInput{Owner: owner,
			IdempotencyKey: c.GetHeader("Idempotency-Key"), Fingerprint: fingerprint, Kind: kind, Model: model,
			Method: c.Request.Method, Path: c.Request.URL.Path})
		if result != nil && result.Operation != nil {
			c.Header("X-DoneBot-Receipt-ID", result.Operation.ReceiptID)
		}
		if response.ErrorFrom(c, err) {
			c.Abort()
			return
		}
		op := result.Operation
		c.Header("Cache-Control", "no-store")
		c.Header(clientRequestIDHeader, op.ReceiptID)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, op.ReceiptID))
		c.Request = c.Request.WithContext(service.WithDoneBotMediaReceipt(c.Request.Context(), op.ReceiptID))
		if result.Replay {
			c.Header("Idempotency-Replayed", "true")
			if op.ReplayRetryAfter != "" {
				c.Header("Retry-After", op.ReplayRetryAfter)
			}
			if kind == "speech" && op.Status == "completed" {
				c.Header("X-DoneBot-Stream-Status", "completed")
			}
			c.Data(op.ReplayStatus, op.ReplayContentType, op.ReplayBody)
			c.Abort()
			return
		}
		writer := &doneBotMediaCapture{ResponseWriter: c.Writer}
		c.Writer = writer
		var validationErr error
		if validator, ok := recovery.(interface {
			ValidateMediaPath(context.Context, service.DoneBotMediaOwner, string, string, string, map[string]any) error
		}); ok {
			validationErr = validator.ValidateMediaPath(c.Request.Context(), owner, kind, model, c.Request.URL.Path, parameters)
		} else {
			validationErr = recovery.ValidateMedia(c.Request.Context(), owner, kind, model, parameters)
		}
		if validationErr == nil {
			if estimator, ok := recovery.(interface {
				EstimateMedia(context.Context, service.DoneBotMediaOwner, string, string, map[string]any) (*json.Number, error)
				SetMediaEstimate(context.Context, service.DoneBotMediaOwner, string, *json.Number) error
			}); ok {
				var estimate *json.Number
				estimate, validationErr = estimator.EstimateMedia(c.Request.Context(), owner, kind, model, parameters)
				if validationErr == nil {
					validationErr = estimator.SetMediaEstimate(c.Request.Context(), owner, op.ReceiptID, estimate)
				}
			}
		}
		if response.ErrorFrom(c, validationErr) {
			c.Abort()
		} else {
			c.Next()
		}
		completion := doneBotMediaCompletion(kind, writer)
		if streamStatus, exists := c.Get(DoneBotMediaStreamStatus); exists {
			completion.Status, _ = streamStatus.(string)
			if completion.Status != "completed" {
				completion.Status = "uncertain"
			}
			if delivered, _ := c.Get(DoneBotMediaStreamDelivered); delivered != true || completion.Status != "completed" {
				completion.Body = nil
			}
		}
		if provider, exists := c.Get(DoneBotMediaProvider); exists {
			completion.Provider, _ = provider.(string)
		}
		usageID, usageRecorded := c.Get(DoneBotMediaUsageRequestID)
		if usageRecorded {
			completion.UsageRequestID, _ = usageID.(string)
		}
		task, taskRecorded := c.Get(DoneBotMediaTaskID)
		if taskRecorded {
			completion.TaskID, _ = task.(string)
		}
		if notDispatched, _ := c.Get(DoneBotMediaNotDispatched); notDispatched == true &&
			completion.HTTPStatus == http.StatusServiceUnavailable && completion.Status == "uncertain" &&
			!taskRecorded && !usageRecorded && completion.TaskID == "" && completion.UsageRequestID == "" {
			completion.Status = "failed"
		}
		if completion.TaskID != "" && completion.Provider == "seedance" {
			completion.TaskKey = service.SeedanceTaskKey(completion.TaskID)
			completion.UsageRequestID = service.StableGrokVideoBillingRequestID(completion.TaskKey)
		}
		if completion.TaskID != "" && (completion.Status == "failed" || completion.Status == "uncertain") {
			completion.Status = "uncertain"
		}
		if completion.TaskID != "" && completion.Provider == "donebot" {
			completion.TaskKey = completion.TaskID
			completion.UsageRequestID = "client:" + op.ReceiptID
		}
		if completion.TaskID != "" && (kind == "image" || kind == "music") && completion.UsageRequestID == "" {
			prefix := "apimart:" + kind + ":"
			completion.TaskKey, completion.UsageRequestID = prefix+completion.TaskID, prefix+completion.TaskID
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 10*time.Second)
		defer cancel()
		if _, err := recovery.CompleteMedia(ctx, owner, op.ReceiptID, completion); err != nil {
			_ = c.Error(err)
		}
	}
}

func doneBotLegacyMediaCreate(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	if strings.HasSuffix(path, "/contents/generations/tasks") {
		return true
	}
	if path == "/v1/videos/generations" || path == "/videos" || strings.HasPrefix(path, "/videos/") || strings.HasPrefix(path, "/images/") || path == "/tts" || path == "/v1/tts" {
		return true
	}
	return false
}

type doneBotMediaCapture struct {
	gin.ResponseWriter
	body     bytes.Buffer
	overflow bool
}

func (w *doneBotMediaCapture) Write(data []byte) (int, error) {
	if !w.overflow {
		if w.body.Len()+len(data) <= service.DoneBotMediaReplayLimit {
			_, _ = w.body.Write(data)
		} else {
			w.overflow = true
			w.body.Reset()
		}
	}
	return w.ResponseWriter.Write(data)
}

func (w *doneBotMediaCapture) WriteString(data string) (int, error) { return w.Write([]byte(data)) }

func doneBotMediaRequest(method, path string) (kind, taskID string) {
	if method == http.MethodPost {
		switch path {
		case "/v1/images/generations", "/v1/images/edits", "/v1/images/generations/async", "/v1/images/edits/async":
			return "image", ""
		case "/v1/videos", "/v1/videos/edits", "/v1/videos/extensions":
			return "video", ""
		case "/v1/music/generations":
			return "music", ""
		case "/v1/audio/speech":
			return "speech", ""
		}
	}
	if method == http.MethodGet {
		for _, prefix := range []string{"/v1/tasks/", "/v1/images/tasks/", "/v1/music/tasks/", "/v1/videos/"} {
			if !strings.HasPrefix(path, prefix) {
				continue
			}
			id := strings.TrimPrefix(path, prefix)
			if id == "" || strings.Contains(id, "/") {
				continue
			}
			kind = "image"
			if prefix == "/v1/music/tasks/" {
				kind = "music"
			}
			if prefix == "/v1/videos/" {
				kind = "video"
			}
			return kind, id
		}
	}
	return "", ""
}

func doneBotMediaParameters(method, path, contentType string, body []byte) (map[string]any, string, error) {
	mediaType, options, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, "", service.ErrDoneBotMediaInvalid
	}
	parameters := map[string]any{}
	if mediaType == "application/json" {
		parameters, err = service.DecodeDoneBotMediaParameters(body)
		if err != nil {
			return nil, "", err
		}
		canonical, err := json.Marshal(parameters)
		if err != nil {
			return nil, "", service.ErrDoneBotMediaInvalid
		}
		return parameters, service.DoneBotMediaFingerprint(method, path, mediaType, canonical), nil
	}
	if mediaType != "multipart/form-data" {
		return nil, "", service.ErrDoneBotMediaInvalid
	}
	reader := multipart.NewReader(bytes.NewReader(body), options["boundary"])
	parts := []string{}
	occurrences := map[string]int{}
	for count := 0; ; count++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil || count >= 128 {
			return nil, "", service.ErrDoneBotMediaInvalid
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, "", service.ErrDoneBotMediaInvalid
		}
		name := part.FormName()
		if name == "" {
			return nil, "", service.ErrDoneBotMediaInvalid
		}
		digest := sha256.Sum256(data)
		ordinal := occurrences[name]
		occurrences[name]++
		parts = append(parts, name+"\x00"+strconv.Itoa(ordinal)+"\x00"+part.FileName()+"\x00"+part.Header.Get("Content-Type")+"\x00"+hex.EncodeToString(digest[:]))
		if part.FileName() == "" {
			if len(data) > 1<<20 {
				return nil, "", service.ErrDoneBotMediaInvalid
			}
			if _, exists := parameters[name]; exists {
				return nil, "", service.ErrDoneBotMediaInvalid
			}
			parameters[name] = string(data)
		}
	}
	sort.Strings(parts)
	canonical, _ := json.Marshal(parts)
	return parameters, service.DoneBotMediaFingerprint(method, path, mediaType, canonical), nil
}

func doneBotMediaCompletion(kind string, writer *doneBotMediaCapture) service.DoneBotMediaCompletion {
	status, body := writer.Status(), writer.body.Bytes()
	c := service.DoneBotMediaCompletion{Status: "uncertain", HTTPStatus: status, ContentType: writer.Header().Get("Content-Type"), RetryAfter: writer.Header().Get("Retry-After")}
	if !writer.overflow {
		c.Body = bytes.Clone(body)
	}
	if status < 200 || status >= 300 {
		if status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != 499 {
			c.Status = "failed"
		}
		return c
	}
	if kind == "speech" || gjson.GetBytes(body, "data.0.url").Exists() || gjson.GetBytes(body, "data.0.b64_json").Exists() {
		c.Status = "completed"
		return c
	}
	for _, path := range []string{"data.0.task_id", "request_id", "id", "task_id"} {
		if id := gjson.GetBytes(body, path).String(); id != "" {
			c.TaskID = id
			break
		}
	}
	if c.TaskID == "" {
		return c
	}
	c.Status = "accepted"
	switch kind {
	case "image":
		c.TaskKey, c.UsageRequestID, c.Provider = "apimart:image:"+c.TaskID, "apimart:image:"+c.TaskID, "apimart"
	case "music":
		c.TaskKey, c.UsageRequestID, c.Provider = "apimart:music:"+c.TaskID, "apimart:music:"+c.TaskID, "apimart"
	case "video":
		c.TaskKey, c.UsageRequestID = c.TaskID, service.StableGrokVideoBillingRequestID(c.TaskID)
	}
	return c
}
