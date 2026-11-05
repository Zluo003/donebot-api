package handler

import (
	"errors"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func streamAPIMartSpeech(c *gin.Context, response *service.APIMartResponse, settle func() error) error {
	c.Set(middleware.DoneBotMediaStreamStatus, "uncertain")
	c.Set(middleware.DoneBotMediaStreamDelivered, false)
	for _, name := range []string{"Content-Type", "Content-Disposition", "X-Request-Id"} {
		if value := response.Headers.Get(name); value != "" {
			c.Header(name, value)
		}
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Trailer", "X-DoneBot-Stream-Status, X-DoneBot-Stream-Error")
	c.Status(response.Status)
	defer response.Stream.Close()
	buffer := make([]byte, 32<<10)
	delivered, readBytes := true, int64(0)
	var upstreamErr error
	for {
		n, err := response.Stream.Read(buffer)
		if n > 0 {
			readBytes += int64(n)
			if delivered {
				written, writeErr := c.Writer.Write(buffer[:n])
				if writeErr != nil || written != n {
					delivered = false
				} else {
					c.Writer.Flush()
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				upstreamErr = err
			}
			break
		}
	}
	if upstreamErr != nil || readBytes == 0 {
		if !c.Writer.Written() {
			c.Writer.Header().Del("Trailer")
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "code": "speech_stream_incomplete", "message": "Upstream audio stream was not completed; do not resubmit this operation"}})
		}
		c.Header("X-DoneBot-Stream-Status", "uncertain")
		c.Header("X-DoneBot-Stream-Error", "upstream_incomplete")
		if upstreamErr != nil {
			return upstreamErr
		}
		return io.ErrUnexpectedEOF
	}
	if err := settle(); err != nil {
		c.Header("X-DoneBot-Stream-Status", "uncertain")
		c.Header("X-DoneBot-Stream-Error", "settlement_unavailable")
		return err
	}
	c.Set(middleware.DoneBotMediaStreamStatus, "completed")
	c.Set(middleware.DoneBotMediaStreamDelivered, delivered)
	c.Header("X-DoneBot-Stream-Status", "completed")
	if !delivered {
		return http.ErrAbortHandler
	}
	return nil
}
