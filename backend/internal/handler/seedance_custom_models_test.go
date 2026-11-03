package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSeedanceCustomMappingAppearsUnderItsDownstreamModelID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformComposite} {
		t.Run(platform, func(t *testing.T) {
			const model = "studio-motion"
			group := &service.Group{ID: 24, Platform: platform, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{model}}}
			repo := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{group.ID: {{
				ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive,
				Schedulable: true, GroupIDs: []int64{group.ID}, Credentials: map[string]any{
					"base_url": service.SeedanceNewTokenBaseURL, "openai_capabilities": []string{"seedance"},
					"model_mapping":        map[string]any{model: "supplier-video-prod"},
					"seedance_model_specs": map[string]any{model: "doubao-seedance-2-5-260628"},
				},
			}}}}
			h := newGatewayModelsHandlerForTest(repo)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{Group: group, GroupID: &group.ID})
			h.Models(c)
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, "list", gjson.GetBytes(w.Body.Bytes(), "object").String())
			require.Equal(t, model, gjson.GetBytes(w.Body.Bytes(), "data.0.id").String())
			require.NotContains(t, w.Body.String(), "supplier-video-prod")
			require.NotContains(t, w.Body.String(), "doubao-seedance-2-5-260628")
		})
	}
}
