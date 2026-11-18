package handler

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const unifiedVideoTaskPrefix = "dbvideo_"

func serveUnifiedVideoTask(c *gin.Context, next gin.HandlerFunc, content bool) bool {
	id := c.Param("request_id")
	if id == "" {
		id = c.Param("task_id")
	}
	if !strings.HasPrefix(id, unifiedVideoTaskPrefix) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, unifiedVideoTaskPrefix))
	if err != nil || len(raw) == 0 || len(raw) > 256 || strings.ContainsAny(string(raw), "/\\\r\n") {
		imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", "Invalid video task ID")
		return true
	}
	params := append(gin.Params(nil), c.Params...)
	defer func() { c.Params = params }()
	for i := range c.Params {
		if c.Params[i].Key == "request_id" || c.Params[i].Key == "task_id" {
			c.Params[i].Value = string(raw)
		}
	}
	if content {
		next(c)
	} else {
		serveUnifiedVideoResponse(c, next, id)
	}
	return true
}

func serveUnifiedVideoResponse(c *gin.Context, next gin.HandlerFunc, publicID string) {
	recorder := httptest.NewRecorder()
	capture, _ := gin.CreateTestContext(recorder)
	writer := c.Writer
	c.Writer = capture.Writer
	func() {
		defer func() { c.Writer = writer }()
		next(c)
	}()
	for k, values := range recorder.Header() {
		c.Writer.Header()[k] = values
	}
	if recorder.Code < 200 || recorder.Code >= 300 {
		c.Data(recorder.Code, "application/json", recorder.Body.Bytes())
		return
	}
	c.Writer.Header().Del("Content-Length")
	c.Writer.Header().Del("Content-Encoding")
	c.Writer.Header().Del("Transfer-Encoding")
	body := recorder.Body.Bytes()
	lookup := publicID != ""
	if !lookup {
		for _, field := range []string{"task_id", "id", "request_id"} {
			if rawID := gjson.GetBytes(body, field).String(); rawID != "" {
				publicID = unifiedVideoTaskPrefix + base64.RawURLEncoding.EncodeToString([]byte(rawID))
				break
			}
		}
		if publicID == "" {
			imageTaskJSONError(c, http.StatusBadGateway, "api_error", "Video submission has no task ID; do not resubmit")
			return
		}
	}
	status := "processing"
	if lookup {
		switch strings.ToLower(gjson.GetBytes(body, "status").String()) {
		case "completed", "succeeded", "success":
			status = "completed"
		case "failed", "error", "cancelled", "canceled", "expired":
			status = "failed"
		case "uncertain":
			status = "uncertain"
		}
	}
	value := gin.H{"id": publicID, "task_id": publicID, "status": status, "content_url": "/v1/videos/" + publicID + "/content"}
	if status == "completed" {
		value["generation_status"] = "completed"
	}
	if status == "failed" || status == "uncertain" {
		value["error"] = gjson.GetBytes(body, "error").Value()
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Retry-After", "3")
	code := http.StatusOK
	if !lookup {
		code = http.StatusAccepted
		c.Header("Location", "/v1/videos/"+publicID)
	}
	c.JSON(code, value)
}
