package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/url"
	"strings"
)

func (s *OpenAIGatewayService) resolveDoneBotDispatchReferences(ctx context.Context, task *DoneBotDispatch) ([]byte, error) {
	if task == nil {
		return nil, ErrDoneBotMediaInvalid
	}
	if len(task.References) == 0 {
		return task.Body, nil
	}
	if task.APIKeyID <= 0 || s == nil || s.generatedMedia == nil {
		return nil, ErrAssetNotFound
	}
	reader, ok := s.generatedMedia.assets.(DoneBotMediaIntentAssetReader)
	if !ok {
		return nil, ErrAssetNotFound
	}
	urls := make(map[string]string, len(task.References))
	types := make(map[string]string, len(task.References))
	for _, reference := range task.References {
		if !doneBotMediaIntentAssetID.MatchString(reference.AssetID) {
			return nil, ErrDoneBotMediaInvalid
		}
		if !doneBotMediaIntentReferenceType(task.Kind, reference.Type) {
			return nil, ErrDoneBotMediaInvalid
		}
		if previousType := types[reference.AssetID]; previousType != "" {
			if previousType != reference.Type {
				return nil, ErrDoneBotMediaInvalid
			}
			continue
		}
		types[reference.AssetID] = reference.Type
		asset, err := reader.Get(ctx, task.APIKeyID, reference.AssetID, "")
		if err != nil || asset == nil || asset.ID != reference.AssetID {
			return nil, ErrAssetNotFound
		}
		assetType, _, err := mime.ParseMediaType(asset.MIME)
		if err != nil || !strings.HasPrefix(strings.ToLower(assetType), reference.Type+"/") {
			return nil, ErrAssetNotFound
		}
		parsed, err := url.Parse(asset.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return nil, ErrAssetNotFound
		}
		urls[reference.AssetID] = asset.URL
	}
	decoder := json.NewDecoder(bytes.NewReader(task.Body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrDoneBotMediaInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrDoneBotMediaInvalid
	}
	used := make(map[string]bool, len(urls))
	resolved, err := replaceDoneBotAssetReferences(value, urls, used)
	if err != nil {
		return nil, err
	}
	for id := range urls {
		if !used[id] {
			return nil, ErrDoneBotMediaInvalid
		}
	}
	body, err := json.Marshal(resolved)
	if err != nil {
		return nil, ErrDoneBotMediaInvalid
	}
	return body, nil
}

func replaceDoneBotAssetReferences(value any, urls map[string]string, used map[string]bool) (any, error) {
	switch typed := value.(type) {
	case string:
		if !strings.HasPrefix(typed, "asset://") {
			return typed, nil
		}
		id := strings.TrimPrefix(typed, "asset://")
		resolved, ok := urls[id]
		if !ok || !doneBotMediaIntentAssetID.MatchString(id) {
			return nil, ErrDoneBotMediaInvalid
		}
		used[id] = true
		return resolved, nil
	case []any:
		for i := range typed {
			child, err := replaceDoneBotAssetReferences(typed[i], urls, used)
			if err != nil {
				return nil, err
			}
			typed[i] = child
		}
		return typed, nil
	case map[string]any:
		for key, item := range typed {
			child, err := replaceDoneBotAssetReferences(item, urls, used)
			if err != nil {
				return nil, err
			}
			typed[key] = child
		}
		return typed, nil
	default:
		return value, nil
	}
}
