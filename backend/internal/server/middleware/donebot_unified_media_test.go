//go:build unit

package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type doneBotUnifiedNormalizer struct {
	native string
	calls  int
}

func (n *doneBotUnifiedNormalizer) NormalizeUnifiedMedia(_ context.Context, _ *service.APIKey, kind string, body []byte) ([]byte, string, error) {
	n.calls++
	converted, path, err := service.ConvertUnifiedMedia(body, kind, n.native)
	if err != nil {
		return nil, "", service.ErrDoneBotMediaInvalid.WithCause(err)
	}
	return converted, path, nil
}

type doneBotUnifiedRecovery struct {
	*doneBotMediaRecoveryStub
	t         *testing.T
	path      string
	estimates int
}

func (r *doneBotUnifiedRecovery) ValidateMediaPath(_ context.Context, _ service.DoneBotMediaOwner, kind, model, path string, parameters map[string]any) error {
	r.validateCalls++
	r.path = path
	require.NotContains(r.t, parameters, "protocol")
	require.NotContains(r.t, parameters, "mode")
	if kind == "image" {
		if path != "/v1/images/generations" {
			return service.ErrDoneBotMediaInvalid
		}
		body, err := json.Marshal(parameters)
		require.NoError(r.t, err)
		info, err := service.ParseAPIMartRequest(service.APIMartImages, body)
		require.NoError(r.t, err)
		require.Equal(r.t, model, info.Model)
		require.Equal(r.t, float64(1), info.Units)
		require.Equal(r.t, "2K", info.Tier)
	} else {
		require.Equal(r.t, "/v1/videos", path)
		require.Equal(r.t, "16:9", parameters["ratio"])
		require.NotContains(r.t, parameters, "aspect_ratio")
		require.NotEmpty(r.t, parameters["content"])
	}
	return nil
}

func (r *doneBotUnifiedRecovery) EstimateMedia(_ context.Context, _ service.DoneBotMediaOwner, _, _ string, parameters map[string]any) (*json.Number, error) {
	r.estimates++
	require.NotContains(r.t, parameters, "protocol")
	return nil, nil
}

func (*doneBotUnifiedRecovery) SetMediaEstimate(context.Context, service.DoneBotMediaOwner, string, *json.Number) error {
	return nil
}

func TestDoneBotUnifiedMediaNormalizesAfterFingerprintBeforeValidation(t *testing.T) {
	for _, tc := range []struct{ kind, spec, path, body string }{
		{"image", "seedream-5-5", "/v1/images/generations/async", `{"protocol":"donebot.media.v1","model":"seedream-5-5","mode":"text","prompt":"An overhead view of a donkey in the desert","resolution":"2K","size":"16:9","count":1,"output_format":"png"}`},
		{"video", "doubao-seedance-2-5-260628", "/v1/videos", `{"protocol":"donebot.media.v1","model":"seedance-2-5","mode":"text","prompt":"waves","resolution":"720p","aspect_ratio":"16:9","duration":6}`},
		{"video", "doubao-seedance-2-5-flash-261115", "/v1/videos", `{"protocol":"donebot.media.v1","model":"seedance-2-5-flash","mode":"reference","prompt":"A person wears the referenced coat at a cafe","resolution":"720p","aspect_ratio":"16:9","duration":5,"output_format":"mp4","references":[{"type":"image","role":"reference","url":"https://assets.invalid/front.webp"},{"type":"image","role":"reference","url":"https://assets.invalid/back.webp"}]}`},
	} {
		t.Run(tc.kind+"/"+tc.spec, func(t *testing.T) {
			recovery := &doneBotUnifiedRecovery{doneBotMediaRecoveryStub: newDoneBotMediaRecoveryStub(), t: t}
			normalizer := &doneBotUnifiedNormalizer{native: tc.spec}
			generated := 0
			router := gin.New()
			router.Use(func(c *gin.Context) {
				groupID := recovery.owner.GroupID
				c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: recovery.owner.APIKeyID, UserID: recovery.owner.UserID, GroupID: &groupID, Key: "sk-donebot-fixture"})
			})
			router.Use(DoneBotMediaRecoveryMiddleware(recovery, normalizer))
			router.POST(tc.path, func(c *gin.Context) {
				generated++
				require.True(t, service.IsUnifiedMediaProtocol(c.Request.Context()))
				require.NotEmpty(t, service.DoneBotMediaReceiptID(c.Request.Context()))
				body, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.NotContains(t, string(body), `"protocol"`)
				if strings.Contains(tc.body, `"output_format":"mp4"`) {
					require.Contains(t, string(body), `"output_format":"mp4"`)
					require.Contains(t, string(body), `"role":"reference_image"`)
				}
				require.Equal(t, recovery.path, c.Request.URL.Path)
				c.Set(DoneBotMediaProvider, "donebot")
				c.JSON(http.StatusAccepted, gin.H{"id": "dbtask_fixture", "status": "processing"})
			})
			server := httptest.NewServer(router)
			defer server.Close()
			for i := 0; i < 2; i++ {
				req, err := http.NewRequest(http.MethodPost, server.URL+tc.path, strings.NewReader(tc.body))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "fixture-request")
				resp, err := server.Client().Do(req)
				require.NoError(t, err)
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				require.Equal(t, http.StatusAccepted, resp.StatusCode, string(body))
				require.NotEmpty(t, resp.Header.Get("X-DoneBot-Receipt-ID"))
				if i == 1 {
					require.Equal(t, "true", resp.Header.Get("Idempotency-Replayed"))
				}
			}
			require.Equal(t, 1, normalizer.calls)
			require.Equal(t, 1, recovery.validateCalls)
			require.Equal(t, 1, recovery.estimates)
			require.Equal(t, 1, generated)
			_, fingerprint, err := doneBotMediaParameters(http.MethodPost, tc.path, "application/json", []byte(tc.body))
			require.NoError(t, err)
			require.Equal(t, fingerprint, recovery.begin[0].Fingerprint)
			require.Equal(t, tc.path, recovery.begin[0].Path)
			require.Equal(t, "accepted", recovery.completion[0].Status)
			invalid := strings.Replace(tc.body, `"mode":"text"`, `"mode":"reference"`, 1)
			if invalid == tc.body {
				invalid = strings.Replace(tc.body, `"mode":"reference"`, `"mode":"text"`, 1)
			}
			refused := doneBotMediaTestRequest(router, http.MethodPost, tc.path, "application/json", "fixture-invalid", []byte(invalid))
			require.Equal(t, http.StatusBadRequest, refused.Code, refused.Body.String())
			require.Equal(t, 1, generated)
			require.Equal(t, 1, recovery.estimates)
			require.Equal(t, "failed", recovery.completion[1].Status)
			conflict := doneBotMediaTestRequest(router, http.MethodPost, tc.path, "application/json", "fixture-request", []byte(invalid))
			require.Equal(t, http.StatusConflict, conflict.Code)
			require.Equal(t, 1, generated)
		})
	}
}
