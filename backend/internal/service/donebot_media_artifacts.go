package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var ErrDoneBotMediaArtifactNotFound = infraerrors.NotFound("DONEBOT_MEDIA_ARTIFACT_NOT_FOUND", "media artifact not found")

type DoneBotDispatchArtifactRepository interface {
	LookupDoneBotDispatchByTaskID(context.Context, string) (*DoneBotDispatch, error)
}

type DoneBotMediaArtifactService struct {
	dispatch DoneBotDispatchArtifactRepository
	assets   *AssetService
	now      func() time.Time
}

func NewDoneBotMediaArtifactService(dispatch DoneBotDispatchArtifactRepository, assets *AssetService) *DoneBotMediaArtifactService {
	return &DoneBotMediaArtifactService{dispatch: dispatch, assets: assets, now: time.Now}
}

func (s *DoneBotMediaArtifactService) Open(ctx context.Context, taskID, artifactID string) (*GatewayAsset, io.ReadCloser, error) {
	if s == nil || s.dispatch == nil || s.assets == nil || !validDoneBotMediaArtifactID(artifactID) {
		return nil, nil, ErrDoneBotMediaArtifactNotFound
	}
	task, err := s.dispatch.LookupDoneBotDispatchByTaskID(ctx, taskID)
	if err != nil || task == nil || !task.DoneBotIntent || task.State != "completed" {
		return nil, nil, ErrDoneBotMediaArtifactNotFound
	}
	var selected *DoneBotMediaArtifact
	for i := range task.Artifacts {
		if task.Artifacts[i].ID == artifactID {
			selected = &task.Artifacts[i]
			break
		}
	}
	if selected == nil || selected.AssetID == "" || !selected.ExpiresAt.After(s.now()) {
		return nil, nil, ErrDoneBotMediaArtifactNotFound
	}
	asset, reader, err := s.assets.OpenOwned(ctx, task.APIKeyID, selected.AssetID)
	if err != nil {
		return nil, nil, ErrDoneBotMediaArtifactNotFound
	}
	if asset.Purpose != AssetPurposeDoneBotOutput {
		_ = reader.Close()
		return nil, nil, ErrDoneBotMediaArtifactNotFound
	}
	return asset, reader, nil
}

type doneBotMediaOutputAssetStore interface {
	GeneratedStoragePolicy(context.Context) (DoneBotStoragePolicy, error)
	UploadDoneBotOutput(context.Context, int64, string, string, []byte) (*GatewayAsset, error)
}

type doneBotMediaOutput struct {
	path     string
	url      string
	b64      string
	dataMIME string
}

func (s *OpenAIGatewayService) PersistDoneBotDispatchArtifacts(ctx context.Context, ownerKeyID int64, taskID, provider, kind string, body []byte) ([]byte, []DoneBotMediaArtifact, error) {
	if s == nil || s.generatedMedia == nil {
		return nil, nil, ErrAssetStorageUnavailable
	}
	assets, ok := s.generatedMedia.assets.(doneBotMediaOutputAssetStore)
	if !ok {
		return nil, nil, ErrAssetStorageUnavailable
	}
	policy, err := assets.GeneratedStoragePolicy(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !policy.Enabled {
		return nil, nil, ErrAssetStorageUnavailable
	}
	return persistDoneBotDispatchArtifacts(ctx, assets, defaultImageDownloadHTTPClient(), ownerKeyID, taskID, provider, kind, body, policy.MaxFileBytes)
}

func persistDoneBotDispatchArtifacts(ctx context.Context, assets doneBotMediaOutputAssetStore, client *http.Client, ownerKeyID int64, taskID, provider, kind string, body []byte, maxBytes int64) ([]byte, []DoneBotMediaArtifact, error) {
	if assets == nil || ownerKeyID <= 0 || taskID == "" || len(body) == 0 || !gjson.ValidBytes(body) {
		return nil, nil, ErrDoneBotMediaInvalid
	}
	outputs := doneBotMediaOutputs(provider, kind, body)
	if len(outputs) == 0 {
		return nil, nil, fmt.Errorf("completed DoneBot media task has no recognized output")
	}
	if client == nil {
		client = defaultImageDownloadHTTPClient()
	}
	artifacts := make([]DoneBotMediaArtifact, 0, len(outputs))
	rewritten := body
	for i, output := range outputs {
		var data []byte
		var contentType string
		var err error
		if output.b64 != "" {
			contentType = output.dataMIME
			if contentType == "" {
				contentType = "image/png"
			}
			if maxBytes > 0 && int64(len(output.b64)) > (maxBytes*4/3)+(1<<20) {
				return nil, nil, ErrAssetTooLarge
			}
			var dataType string
			data, dataType, err = decodeDoneBotMediaOutput(output.b64)
			if dataType != "" {
				contentType = dataType
			}
		} else {
			uploader := generatedMediaUploader{client: client}
			data, contentType, err = uploader.download(ctx, output.url, maxBytes)
		}
		if err != nil {
			return nil, nil, err
		}
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return nil, nil, ErrAssetTooLarge
		}
		if !strings.HasPrefix(contentType, kind+"/") {
			return nil, nil, fmt.Errorf("generated media content type does not match the requested media kind")
		}
		name := fmt.Sprintf("%s-%02d%s", safeDoneBotArtifactBase(taskID), i+1, doneBotMediaExtension(contentType))
		asset, err := assets.UploadDoneBotOutput(ctx, ownerKeyID, name, contentType, data)
		if err != nil {
			return nil, nil, err
		}
		artifactID := uuid.NewString()
		artifacts = append(artifacts, DoneBotMediaArtifact{
			ID: artifactID, AssetID: asset.ID, Kind: strings.SplitN(contentType, "/", 2)[0], MIME: contentType,
			Name: name, ExpiresAt: asset.ExpiresAt,
		})
		artifactURL := DoneBotMediaArtifactURL(taskID, artifactID)
		if output.b64 != "" {
			if strings.HasSuffix(output.path, ".b64_json") {
				rewritten, _ = sjson.DeleteBytes(rewritten, output.path)
				rewritten, err = sjson.SetBytes(rewritten, strings.TrimSuffix(output.path, ".b64_json")+".url", artifactURL)
			} else {
				rewritten, err = sjson.SetBytes(rewritten, output.path, artifactURL)
			}
		} else {
			rewritten, err = sjson.SetBytes(rewritten, output.path, artifactURL)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return rewritten, artifacts, nil
}

func doneBotMediaOutputs(provider, kind string, body []byte) []doneBotMediaOutput {
	var outputs []doneBotMediaOutput
	addURL := func(path string, index int) {
		value := gjson.GetBytes(body, path)
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			outputs = append(outputs, doneBotMediaOutput{path: path, url: value.String()})
		}
	}
	addImageItems := func(prefix string) bool {
		items := gjson.GetBytes(body, prefix)
		if !items.IsArray() {
			return false
		}
		for index, item := range items.Array() {
			base := fmt.Sprintf("%s.%d", prefix, index)
			b64 := strings.TrimSpace(item.Get("b64_json").String())
			if b64 != "" {
				outputs = append(outputs, doneBotMediaOutput{path: base + ".b64_json", b64: b64, dataMIME: doneBotImageOutputMIME(item.Get("output_format").String())})
				continue
			}
			urlValue := item.Get("url")
			if urlValue.IsArray() {
				for urlIndex, value := range urlValue.Array() {
					path := fmt.Sprintf("%s.url.%d", base, urlIndex)
					if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
						outputs = append(outputs, doneBotMediaOutput{path: path, url: value.String()})
					}
				}
			} else if urlValue.Type == gjson.String && strings.TrimSpace(urlValue.String()) != "" {
				outputs = append(outputs, doneBotMediaOutput{path: base + ".url", url: urlValue.String()})
			}
		}
		return len(outputs) > 0
	}
	switch kind {
	case "image":
		if addImageItems("data") {
			return outputs
		}
		for _, prefix := range []string{"result.images", "data.result.images"} {
			addImageItems(prefix)
			if len(outputs) > 0 {
				break
			}
		}
	case "video":
		if provider == "seedance" {
			addURL("content.video_url", 0)
		}
	case "audio":
		if provider == "apimart" {
			for _, prefix := range []string{"result.music", "data.result.music"} {
				for index, item := range gjson.GetBytes(body, prefix).Array() {
					base := fmt.Sprintf("%s.%d", prefix, index)
					if value := strings.TrimSpace(item.Get("audio_url").String()); value != "" {
						outputs = append(outputs, doneBotMediaOutput{path: base + ".audio_url", url: value})
					}
				}
				if len(outputs) > 0 {
					break
				}
			}
		}
	}
	return outputs
}

func decodeDoneBotMediaOutput(value string) ([]byte, string, error) {
	contentType := ""
	if strings.HasPrefix(value, "data:") {
		comma := strings.IndexByte(value, ',')
		if comma < 0 || !strings.Contains(value[:comma], ";base64") {
			return nil, "", ErrDoneBotMediaInvalid
		}
		contentType, _, _ = mime.ParseMediaType(strings.TrimPrefix(strings.TrimSuffix(value[:comma], ";base64"), "data:"))
		data, err := base64.StdEncoding.DecodeString(value[comma+1:])
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(value[comma+1:])
		}
		return data, contentType, err
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(value)
	}
	return data, contentType, err
}

func doneBotImageOutputMIME(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	default:
		return "image/png"
	}
}

func doneBotMediaExtension(contentType string) string {
	extensions, _ := mime.ExtensionsByType(contentType)
	for _, extension := range extensions {
		if filepath.Ext(extension) == extension && len(extension) <= 8 {
			return extension
		}
	}
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "audio/mpeg":
		return ".mp3"
	case "video/mp4":
		return ".mp4"
	default:
		return ".bin"
	}
}

func safeDoneBotArtifactBase(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 48 {
		value = value[:48]
	}
	value = strings.Trim(value, "-_.")
	if value == "" {
		return "donebot"
	}
	return value
}

func validDoneBotMediaArtifactID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
