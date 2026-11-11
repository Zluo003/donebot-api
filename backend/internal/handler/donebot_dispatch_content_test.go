//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type doneBotVideoAssetRepo struct {
	service.AssetRepository
	asset *service.GatewayAsset
	owner int64
}

func (r *doneBotVideoAssetRepo) GetByID(_ context.Context, id string) (*service.GatewayAsset, error) {
	if id != r.asset.ID {
		return nil, service.ErrAssetNotFound
	}
	return r.asset, nil
}

func (r *doneBotVideoAssetRepo) HasOwner(_ context.Context, id string, owner int64) (bool, error) {
	return id == r.asset.ID && owner == r.owner, nil
}

type doneBotVideoFileStorage struct {
	service.AssetStorage
	path string
}

func (*doneBotVideoFileStorage) Kind() string { return "local" }
func (s *doneBotVideoFileStorage) Open(context.Context, string) (io.ReadCloser, error) {
	return os.Open(s.path)
}

func TestDoneBotDispatchVideoContentSurvivesRestartAndAccountFailover(t *testing.T) {
	db, sql, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	money := repository.NewUsageBillingRepository(nil, db)
	path := filepath.Join(t.TempDir(), "video.mp4")
	require.NoError(t, os.WriteFile(path, []byte("fixture-video-bytes"), 0600))
	assets := &doneBotVideoAssetRepo{owner: 20, asset: &service.GatewayAsset{
		ID: "ast_video", MIME: "video/mp4", Size: 19, Storage: "local", Purpose: service.AssetPurposeGenerated,
		ObjectKey: "generated/video", CreatedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour),
	}}
	task := service.DoneBotDispatch{TaskID: "dbtask_fixture", UserID: 10, APIKeyID: 20, GroupID: 24,
		Protocol: service.DoneBotDispatchSeedance, Kind: "video", State: "completed", AccountID: 6,
		ProviderTaskID: "provider-final", Attempts: []service.DoneBotDispatchAttempt{
			{AccountID: 5, Status: "failed"}, {AccountID: 6, ProviderTaskID: "provider-final", Status: "completed"},
		}, Response: []byte(`{"status":"succeeded","content":{"video_url":"https://retired.example/assets/ast_video?token=1.expired"}}`)}
	raw, err := json.Marshal(task)
	require.NoError(t, err)
	for _, restarted := range []bool{false, true} {
		h, slots, bindings, upstream := newGrokMediaSlotHandlerWithBilling(t, money, false, false, service.PlatformOpenAI)
		h.gatewayService.SetGeneratedMediaAssets(service.NewAssetService(assets, &doneBotVideoFileStorage{path: path}, &config.Config{}))
		bindings.jobs = nil
		router := gin.New()
		router.GET("/v1/videos/:request_id", func(c *gin.Context) { setDoneBotContentOwner(c, 10, 20) }, h.DoneBotVideoStatus)
		router.GET("/v1/videos/:request_id/content", func(c *gin.Context) { setDoneBotContentOwner(c, 10, 20) }, h.DoneBotVideoContent)
		server := httptest.NewServer(router)
		func() {
			defer server.Close()
			expectDoneBotContentLookup(sql, raw, 10, 20, task.TaskID)
			response, err := server.Client().Get(server.URL + "/v1/videos/" + task.TaskID)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, http.StatusOK, response.StatusCode)
			var status map[string]any
			require.NoError(t, json.Unmarshal(body, &status))
			require.Equal(t, task.TaskID, status["id"])
			require.Equal(t, "succeeded", status["status"])
			require.Equal(t, "https://retired.example/assets/ast_video?token=1.expired", status["content"].(map[string]any)["video_url"])
			for _, partial := range []bool{false, true} {
				expectDoneBotContentLookup(sql, raw, 10, 20, task.TaskID)
				request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/videos/"+task.TaskID+"/content", nil)
				require.NoError(t, err)
				if partial {
					request.Header.Set("Range", "bytes=0-6")
				}
				response, err := server.Client().Do(request)
				require.NoError(t, err)
				body, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				if partial {
					require.Equal(t, http.StatusPartialContent, response.StatusCode)
					require.Equal(t, "fixture", string(body))
					require.Equal(t, "bytes 0-6/19", response.Header.Get("Content-Range"))
				} else {
					require.Equal(t, http.StatusOK, response.StatusCode, "restarted=%v", restarted)
					require.Equal(t, "fixture-video-bytes", string(body))
				}
				require.Equal(t, "video/mp4", response.Header.Get("Content-Type"))
				require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
				require.Empty(t, response.Header.Get("Location"))
			}
		}()
		require.Zero(t, upstream.calls, "completed downloads must not contact the model provider")
		slots.assertReleased(t)
		require.NoError(t, sql.ExpectationsWereMet(), "download cannot mutate billing or dispatch records")
	}
}

func TestDoneBotDispatchVideoContentEnforcesOwnershipAndState(t *testing.T) {
	for _, scenario := range []string{"other user", "other key", "missing", "unavailable", "queued", "polling", "retrieving", "settling", "uncertain", "failed", "wrong protocol", "expired asset", "unowned asset"} {
		t.Run(scenario, func(t *testing.T) {
			db, sql, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			h, slots, bindings, upstream := newGrokMediaSlotHandlerWithBilling(t, repository.NewUsageBillingRepository(nil, db), false, false, service.PlatformOpenAI)
			bindings.jobs = nil
			task := service.DoneBotDispatch{TaskID: "dbtask_fixture", UserID: 10, APIKeyID: 20,
				Protocol: service.DoneBotDispatchSeedance, Kind: "video", State: "completed",
				Response: []byte(`{"status":"succeeded","content":{"video_url":"https://gateway.invalid/assets/ast_video?token=1.expired"}}`)}
			assets := &doneBotVideoAssetRepo{owner: 20, asset: &service.GatewayAsset{ID: "ast_video", MIME: "video/mp4", Storage: "local", ExpiresAt: time.Now().Add(time.Hour)}}
			h.gatewayService.SetGeneratedMediaAssets(service.NewAssetService(assets, &doneBotVideoFileStorage{}, &config.Config{}))
			user, key, expected := int64(10), int64(20), http.StatusConflict
			switch scenario {
			case "other user":
				user, expected = 11, http.StatusNotFound
			case "other key":
				key, expected = 21, http.StatusNotFound
			case "missing":
				expected = http.StatusNotFound
			case "unavailable":
				expected = http.StatusInternalServerError
			case "wrong protocol":
				task.Protocol, expected = service.DoneBotDispatchAPIMartMusic, http.StatusNotFound
			case "expired asset":
				assets.asset.ExpiresAt, expected = time.Now().Add(-time.Hour), http.StatusNotFound
			case "unowned asset":
				assets.owner, expected = 21, http.StatusNotFound
			default:
				task.State = scenario
			}
			raw, err := json.Marshal(task)
			require.NoError(t, err)
			query := sql.ExpectQuery(`SELECT payload FROM donebot_media_dispatches WHERE user_id=\$1 AND api_key_id=\$2 AND task_id=\$3`).WithArgs(user, key, task.TaskID)
			if scenario == "other user" || scenario == "other key" || scenario == "missing" {
				query.WillReturnRows(sqlmock.NewRows([]string{"payload"}))
			} else if scenario == "unavailable" {
				query.WillReturnError(errors.New("database offline"))
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(raw))
			}
			c, w := doneBotVideoContext(http.MethodGet, "/v1/videos/"+task.TaskID+"/content", task.TaskID)
			setDoneBotContentOwner(c, user, key)
			h.DoneBotVideoContent(c)
			require.Equal(t, expected, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), "gateway.invalid")
			require.Empty(t, w.Header().Get("Location"))
			if expected == http.StatusConflict && scenario != "failed" {
				require.Equal(t, "3", w.Header().Get("Retry-After"))
			} else {
				require.Empty(t, w.Header().Get("Retry-After"))
			}
			require.Zero(t, upstream.calls)
			slots.assertReleased(t)
			require.NoError(t, sql.ExpectationsWereMet())
		})
	}
}

func expectDoneBotContentLookup(sql sqlmock.Sqlmock, raw []byte, user, key int64, id string) {
	sql.ExpectQuery(`SELECT payload FROM donebot_media_dispatches WHERE user_id=\$1 AND api_key_id=\$2 AND task_id=\$3`).
		WithArgs(user, key, id).WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(raw))
}

func setDoneBotContentOwner(c *gin.Context, user, keyID int64) {
	group := int64(24)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: keyID, UserID: user, Key: "sk-donebot-fixture", GroupID: &group,
		Group: &service.Group{ID: group, Platform: service.PlatformComposite}, User: &service.User{ID: user}})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user})
	c.Next()
}
