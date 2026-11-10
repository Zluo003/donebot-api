//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type doneBotDispatchAssetStub struct {
	owner int64
	asset *GatewayAsset
	err   error
}

func (*doneBotDispatchAssetStub) GeneratedStoragePolicy(context.Context) (DoneBotStoragePolicy, error) {
	return DoneBotStoragePolicy{Enabled: true}, nil
}

func (*doneBotDispatchAssetStub) UploadGenerated(context.Context, int64, string, string, []byte, string) (*GatewayAsset, error) {
	return nil, errors.New("unexpected output upload")
}

func (s *doneBotDispatchAssetStub) Get(_ context.Context, owner int64, id, _ string) (*GatewayAsset, error) {
	if owner != s.owner || s.asset == nil || s.asset.ID != id {
		return nil, ErrAssetNotFound
	}
	return s.asset, s.err
}

func TestDoneBotDispatchReferencesResolveOnlyForOwnerAndNeverPersistSignedURLs(t *testing.T) {
	id := "ast_0123456789abcdef0123456789abcdef"
	assetReader := &doneBotDispatchAssetStub{owner: 9, asset: &GatewayAsset{ID: id, MIME: "image/png", URL: "https://assets.example/input.png?token=secret"}}
	service := &OpenAIGatewayService{generatedMedia: &generatedMediaUploader{assets: assetReader}}
	task := &DoneBotDispatch{
		APIKeyID: 9, Kind: "image", DoneBotIntent: true,
		References: []DoneBotMediaIntentReference{{AssetID: id, Type: "image"}, {AssetID: id, Type: "image"}},
		Body:       []byte(`{"model":"image","images":[{"image_url":"asset://` + id + `"},{"image_url":"asset://` + id + `"}]}`),
	}

	requestBody, err := service.resolveDoneBotDispatchReferences(context.Background(), task)
	require.NoError(t, err)
	require.Contains(t, string(requestBody), "https://assets.example/input.png?token=secret")
	require.NotContains(t, string(task.Body), "https://assets.example")
	require.Contains(t, string(task.Body), "asset://"+id)

	raw, err := json.Marshal(task)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "token=secret")
}

func TestDoneBotDispatchReferencesRejectWrongOwnerAndMissingPlaceholder(t *testing.T) {
	id := "ast_0123456789abcdef0123456789abcdef"
	reader := &doneBotDispatchAssetStub{owner: 10, asset: &GatewayAsset{ID: id, MIME: "image/png", URL: "https://assets.example/input.png"}}
	service := &OpenAIGatewayService{generatedMedia: &generatedMediaUploader{assets: reader}}
	task := &DoneBotDispatch{APIKeyID: 9, Kind: "image", References: []DoneBotMediaIntentReference{{AssetID: id, Type: "image"}}, Body: []byte(`{"model":"image","prompt":"draw"}`)}
	_, err := service.resolveDoneBotDispatchReferences(context.Background(), task)
	require.ErrorIs(t, err, ErrAssetNotFound)

	reader.owner = task.APIKeyID
	_, err = service.resolveDoneBotDispatchReferences(context.Background(), task)
	require.ErrorIs(t, err, ErrDoneBotMediaInvalid)
}
