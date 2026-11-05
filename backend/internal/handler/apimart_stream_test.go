package handler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAPIMartSpeechFlushesBeforeUpstreamCompletes(t *testing.T) {
	reader, writer := io.Pipe()
	release := make(chan struct{})
	defer close(release)
	go func() {
		_, _ = writer.Write([]byte("first"))
		<-release
		_, _ = writer.Write([]byte("last"))
		_ = writer.Close()
	}()
	var settled atomic.Int32
	router := gin.New()
	router.POST("/speech", func(c *gin.Context) {
		_ = streamAPIMartSpeech(c, &service.APIMartResponse{Status: 200, Headers: http.Header{"Content-Type": []string{"audio/opus"}}, Stream: reader}, func() error {
			settled.Add(1)
			return nil
		})
	})
	server := httptest.NewServer(router)
	defer server.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(server.URL+"/speech", "application/json", nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	first := make([]byte, 5)
	_, err = io.ReadFull(resp.Body, first)
	require.NoError(t, err)
	require.Equal(t, "first", string(first))
	require.Zero(t, settled.Load(), "billing must wait for complete upstream audio")
	release <- struct{}{}
	last, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "last", string(last))
	require.Equal(t, "completed", resp.Trailer.Get("X-DoneBot-Stream-Status"))
	require.EqualValues(t, 1, settled.Load())
}

type speechFailingReader struct{ sent bool }

func (r *speechFailingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "partial"), nil
	}
	return 0, io.ErrUnexpectedEOF
}
func (*speechFailingReader) Close() error { return nil }

func TestAPIMartSpeechIncompleteAndSettlementErrorsAreTrailers(t *testing.T) {
	for _, test := range []struct {
		name, code string
		stream     io.ReadCloser
		settleErr  error
		settles    int
	}{
		{"upstream", "upstream_incomplete", &speechFailingReader{}, nil, 0},
		{"billing", "settlement_unavailable", io.NopCloser(strings.NewReader("audio")), errors.New("billing unavailable"), 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			calls := 0
			err := streamAPIMartSpeech(c, &service.APIMartResponse{Status: 200, Headers: http.Header{"Content-Type": []string{"audio/opus"}}, Stream: test.stream}, func() error { calls++; return test.settleErr })
			require.Error(t, err)
			require.Equal(t, test.settles, calls)
			resp := w.Result()
			require.Equal(t, "uncertain", resp.Trailer.Get("X-DoneBot-Stream-Status"))
			require.Equal(t, test.code, resp.Trailer.Get("X-DoneBot-Stream-Error"))
		})
	}
}
