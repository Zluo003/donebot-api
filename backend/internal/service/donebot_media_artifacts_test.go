package service

import (
	"context"
	"encoding/base64"
	"io"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type doneBotArtifactStoreStub struct {
	policy DoneBotStoragePolicy
	asset  *GatewayAsset
	data   []byte
}

func (s *doneBotArtifactStoreStub) GeneratedStoragePolicy(context.Context) (DoneBotStoragePolicy, error) {
	return s.policy, nil
}

func (s *doneBotArtifactStoreStub) UploadDoneBotOutput(_ context.Context, owner int64, name, contentType string, data []byte) (*GatewayAsset, error) {
	s.data = append([]byte(nil), data...)
	createdAt := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	s.asset = &GatewayAsset{ID: "ast_0123456789abcdef0123456789abcdef", OwnerKeyID: owner, Purpose: AssetPurposeDoneBotOutput, MIME: contentType, CreatedAt: createdAt, ExpiresAt: createdAt.Add(time.Duration(s.policy.TTLHours) * time.Hour)}
	return s.asset, nil
}

type doneBotArtifactDispatchStub struct{ task *DoneBotDispatch }

func (s doneBotArtifactDispatchStub) LookupDoneBotDispatchByTaskID(_ context.Context, taskID string) (*DoneBotDispatch, error) {
	if s.task == nil || s.task.TaskID != taskID {
		return nil, ErrDoneBotMediaArtifactNotFound
	}
	return s.task, nil
}

func TestPersistDoneBotDispatchArtifactsStoresOutputsAndRewritesURLs(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfixture")
	assets := &doneBotArtifactStoreStub{policy: DoneBotStoragePolicy{Enabled: true, TTLHours: 6, MaxFileBytes: 1024}}
	body := []byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(png) + `","revised_prompt":"keep"}]}`)
	got, artifacts, err := persistDoneBotDispatchArtifacts(context.Background(), assets, nil, 7, "dbtask_fixture", "openai", "image", body, 1024)
	require.NoError(t, err)
	require.Equal(t, png, assets.data)
	require.Len(t, artifacts, 1)
	require.Equal(t, AssetPurposeDoneBotOutput, assets.asset.Purpose)
	require.Equal(t, assets.asset.CreatedAt.Add(6*time.Hour), artifacts[0].ExpiresAt)
	require.Regexp(t, `^[0-9a-f-]{36}$`, artifacts[0].ID)
	require.Equal(t, "keep", gjson.GetBytes(got, "data.0.revised_prompt").String())
	require.Empty(t, gjson.GetBytes(got, "data.0.b64_json").String())
	require.Equal(t, DoneBotMediaArtifactURL("dbtask_fixture", artifacts[0].ID), gjson.GetBytes(got, "data.0.url").String())
}

func TestDoneBotMediaArtifactOpenChecksBindingExpiryAndPurpose(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 30, 0, 0, time.UTC)
	artifact := DoneBotMediaArtifact{ID: "93f6fc07-c0b0-4dfa-a2d8-10d588eb127f", AssetID: "ast_output", Kind: "image", MIME: "image/png", ExpiresAt: now.Add(30 * time.Minute)}
	task := &DoneBotDispatch{TaskID: "dbtask_fixture", APIKeyID: 42, DoneBotIntent: true, State: "completed", Artifacts: []DoneBotMediaArtifact{artifact}}
	repo := &assetTestRepo{
		rows:   map[string]*GatewayAsset{"ast_output": {ID: "ast_output", Purpose: AssetPurposeDoneBotOutput, MIME: "image/png", Storage: "local", ObjectKey: "donebot_output/output.png", ExpiresAt: now.Add(time.Hour)}},
		owners: map[string]map[int64]bool{"ast_output": {42: true}},
	}
	assets := NewAssetService(repo, &assetTestStorage{}, &config.Config{})
	assets.now = func() time.Time { return now }
	service := NewDoneBotMediaArtifactService(doneBotArtifactDispatchStub{task: task}, assets)
	service.now = func() time.Time { return now }
	asset, reader, err := service.Open(context.Background(), task.TaskID, artifact.ID)
	require.NoError(t, err)
	require.Equal(t, AssetPurposeDoneBotOutput, asset.Purpose)
	_, err = io.Copy(io.Discard, reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())

	_, _, err = service.Open(context.Background(), "dbtask_other", artifact.ID)
	require.ErrorIs(t, err, ErrDoneBotMediaArtifactNotFound)
	_, _, err = service.Open(context.Background(), task.TaskID, "a2ad363e-878d-4d19-b743-77da20ed2359")
	require.ErrorIs(t, err, ErrDoneBotMediaArtifactNotFound)
	task.DoneBotIntent = false
	_, _, err = service.Open(context.Background(), task.TaskID, artifact.ID)
	require.ErrorIs(t, err, ErrDoneBotMediaArtifactNotFound)
	task.DoneBotIntent = true
	service.now = func() time.Time { return artifact.ExpiresAt }
	_, _, err = service.Open(context.Background(), task.TaskID, artifact.ID)
	require.ErrorIs(t, err, ErrDoneBotMediaArtifactNotFound)
}
