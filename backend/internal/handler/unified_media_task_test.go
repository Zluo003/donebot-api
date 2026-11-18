//go:build unit

package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUnifiedAPIMartImageLifecycleAndOwnership(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, "apimart")
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		body := `{"code":200,"data":[{"task_id":"task-mart"}]}`
		if req.Method == http.MethodPost {
			data, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.NotContains(t, string(data), `"protocol"`)
			require.Equal(t, "waves", gjson.GetBytes(data, "prompt").String())
		} else {
			body = `{"code":200,"data":{"status":"completed","result":{"images":[{"url":["https://assets.invalid/output.png"]}]}}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	tasks := service.NewImageTaskServiceWithUploader(&asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{}}, nil, time.Hour, time.Minute)
	async := &AsyncImageHandler{openAI: h, tasks: tasks, execute: func(string, *gin.Context) { t.Fatal("unexpected synchronous dispatch") }}
	c, w := apimartHandlerContext(`{"protocol":"donebot.media.v1","model":"seedream-5-0-flash","mode":"text","prompt":"waves","size":"2K"}`, false)
	c.Request.URL.Path = "/v1/images/generations/async"
	async.Submit(c)
	require.Equal(t, 202, w.Code, w.Body.String())
	id := gjson.GetBytes(w.Body.Bytes(), "id").String()
	require.True(t, strings.HasPrefix(id, "apimart_"))
	c, w = apimartHandlerContext("", true)
	c.Params[0].Value = id
	async.Get(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "completed", gjson.GetBytes(w.Body.Bytes(), "status").String())
	require.Equal(t, "https://assets.invalid/output.png", gjson.GetBytes(w.Body.Bytes(), "result.data.0.url").String())
	c, w = apimartHandlerContext("", true)
	c.Params[0].Value = id
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.ID++
	before := upstream.calls
	async.Get(c)
	require.Equal(t, 404, w.Code, w.Body.String())
	require.Equal(t, before, upstream.calls)
	slots.assertReleased(t)
}

func TestUnifiedVideoResponseNormalizesSupplierStates(t *testing.T) {
	for _, state := range []string{"running", "succeeded", "failed", "uncertain"} {
		t.Run(state, func(t *testing.T) {
			c, w := museHandlerContext(http.MethodGet, "fixture")
			serveUnifiedVideoResponse(c, func(c *gin.Context) { c.JSON(200, gin.H{"status": state}) }, "public-id")
			var response map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			want := state
			if state == "running" {
				want = "processing"
			}
			if state == "succeeded" {
				want = "completed"
			}
			require.Equal(t, want, response["status"])
			require.Equal(t, "/v1/videos/public-id/content", response["content_url"])
		})
	}
}
