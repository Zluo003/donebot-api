package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func (h *AsyncImageHandler) serveUnifiedAPIMartImage(c *gin.Context, lookup bool) {
	publicID := c.Param("task_id")
	if lookup {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(publicID, "apimart_"))
		if err != nil || len(raw) == 0 || len(raw) > 256 || strings.ContainsAny(string(raw), "/\\\r\n") {
			imageTaskJSONError(c, 400, "invalid_request_error", "Invalid image task ID")
			return
		}
		params := append(gin.Params(nil), c.Params...)
		defer func() { c.Params = params }()
		for i := range c.Params {
			if c.Params[i].Key == "task_id" {
				c.Params[i].Value = string(raw)
			}
		}
	}
	recorder := httptest.NewRecorder()
	capture, _ := gin.CreateTestContext(recorder)
	writer := c.Writer
	c.Writer = capture.Writer
	func() {
		defer func() { c.Writer = writer }()
		if lookup {
			h.openAI.APIMartImageTask(c)
		} else {
			h.openAI.APIMartImages(c)
		}
	}()
	for k, values := range recorder.Header() {
		for _, v := range values {
			c.Writer.Header().Add(k, v)
		}
	}
	if recorder.Code < 200 || recorder.Code >= 300 {
		c.Data(recorder.Code, "application/json", recorder.Body.Bytes())
		return
	}
	c.Writer.Header().Del("Content-Length")
	c.Writer.Header().Del("Content-Encoding")
	c.Writer.Header().Del("Transfer-Encoding")
	body := recorder.Body.Bytes()
	status := "processing"
	if !lookup {
		rawID := gjson.GetBytes(body, "data.0.task_id").String()
		if rawID == "" {
			imageTaskJSONError(c, 502, "api_error", "Image submission has no task ID; do not resubmit")
			return
		}
		publicID = "apimart_" + base64.RawURLEncoding.EncodeToString([]byte(rawID))
	} else {
		switch gjson.GetBytes(body, "data.status").String() {
		case "completed", "succeeded", "success":
			status = "completed"
		case "failed", "error", "cancelled":
			status = "failed"
		}
	}
	value := gin.H{"id": publicID, "task_id": publicID, "status": status}
	if status == "completed" {
		value["result"] = json.RawMessage(service.UnifiedAPIMartImageResult(body))
	}
	if status == "failed" {
		failure := gjson.GetBytes(body, "data.error")
		if !failure.Exists() {
			failure = gjson.GetBytes(body, "error")
		}
		value["error"] = failure.Value()
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Retry-After", "3")
	code := http.StatusOK
	if !lookup {
		code = http.StatusAccepted
		c.Header("Location", "/v1/images/tasks/"+publicID)
	}
	c.JSON(code, value)
}
