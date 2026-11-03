package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"golang.org/x/sync/singleflight"
)

type generatedMediaAssets interface {
	GeneratedStoragePolicy(context.Context) (DoneBotStoragePolicy, error)
	UploadGenerated(context.Context, int64, string, string, []byte, string) (*GatewayAsset, error)
}

// Media is stored through the same asset retention service as async images.
// Only native media URL fields are changed; model metadata stays untouched.
type generatedMediaUploader struct {
	assets generatedMediaAssets
	client *http.Client
	cache  GatewayCache
	flight singleflight.Group
}

func (s *OpenAIGatewayService) SetGeneratedMediaAssets(assets *AssetService) {
	if assets != nil {
		s.generatedMedia = &generatedMediaUploader{assets: assets, client: defaultImageDownloadHTTPClient(), cache: s.cache}
	}
}

func (s *OpenAIGatewayService) GeneratedMediaStorageEnabled(ctx context.Context) (bool, error) {
	if s == nil || s.generatedMedia == nil {
		return false, nil
	}
	p, err := s.generatedMedia.assets.GeneratedStoragePolicy(ctx)
	if p.Driver == "local" && strings.TrimSpace(p.PublicBaseURL) == "" {
		return false, err
	}
	return p.Enabled, err
}

func (s *OpenAIGatewayService) RewriteGeneratedMedia(ctx context.Context, owner int64, taskKey, provider string, body []byte) ([]byte, error) {
	if s == nil || s.generatedMedia == nil {
		return body, nil
	}
	return s.generatedMedia.rewrite(ctx, owner, taskKey, provider, body)
}

func (u *generatedMediaUploader) rewrite(ctx context.Context, owner int64, taskKey, provider string, body []byte) ([]byte, error) {
	p, err := u.assets.GeneratedStoragePolicy(ctx)
	if err != nil {
		return nil, err
	}
	if !p.Enabled {
		return body, nil
	}
	paths := generatedMediaPaths(provider, body)
	if len(paths) == 0 {
		return body, nil
	}
	if owner <= 0 {
		return nil, ErrAssetNotFound
	}
	digest := sha256.Sum256([]byte(provider + ":" + taskKey + ":" + strconv.FormatInt(owner, 10)))
	cacheKey := "generated-media:" + hex.EncodeToString(digest[:])
	value, err, _ := u.flight.Do(cacheKey, func() (any, error) {
		// Persist replacements, not the entire provider response: a later poll
		// retains fresh native metadata and avoids downloading the media again.
		links := map[string]string{}
		if u.cache != nil {
			raw, e := u.cache.GetGrokVideoPendingBilling(ctx, cacheKey)
			if e != nil {
				return nil, e
			}
			if len(raw) > 0 {
				if e = json.Unmarshal(raw, &links); e != nil {
					return nil, e
				}
			}
		}
		changed := false
		for _, path := range paths {
			if links[path] != "" {
				continue
			}
			source := gjson.GetBytes(body, path).String()
			data, contentType, e := u.download(ctx, source, p.MaxFileBytes)
			if e != nil {
				return nil, e
			}
			asset, e := u.assets.UploadGenerated(ctx, owner, taskKey+"-"+path, contentType, data, "")
			if e != nil {
				return nil, e
			}
			links[path] = asset.URL
			changed = true
		}
		if changed && u.cache != nil {
			raw, e := json.Marshal(links)
			if e != nil {
				return nil, e
			}
			ttl := time.Duration(p.TTLHours) * time.Hour
			if ttl <= 0 {
				ttl = 7 * 24 * time.Hour
			}
			if e = u.cache.SetGrokVideoPendingBilling(ctx, cacheKey, raw, ttl); e != nil {
				return nil, e
			}
		}
		return links, nil
	})
	if err != nil {
		return nil, err
	}
	links, ok := value.(map[string]string)
	if !ok {
		return nil, fmt.Errorf("invalid generated media replacements")
	}
	for path, link := range links {
		if !gjson.GetBytes(body, path).Exists() {
			continue
		}
		body, err = sjson.SetBytes(body, path, link)
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

func generatedMediaPaths(provider string, body []byte) []string {
	var paths []string
	add := func(path string) {
		if v := gjson.GetBytes(body, path); v.Type == gjson.String && v.String() != "" {
			paths = append(paths, path)
		}
	}
	if provider == "seedance" {
		add("content.video_url")
		add("content.last_frame_url")
		return paths
	}
	if provider != "apimart" {
		return nil
	}
	for _, prefix := range []string{"result", "data.result"} {
		for i := range gjson.GetBytes(body, prefix+".music").Array() {
			for _, field := range []string{"audio_url", "image_url"} {
				add(prefix + ".music." + strconv.Itoa(i) + "." + field)
			}
		}
		for i, image := range gjson.GetBytes(body, prefix+".images").Array() {
			path := prefix + ".images." + strconv.Itoa(i) + ".url"
			if image.Get("url").IsArray() {
				for n := range image.Get("url").Array() {
					add(path + "." + strconv.Itoa(n))
				}
			} else {
				add(path)
			}
		}
	}
	return paths
}

func (u *generatedMediaUploader) download(ctx context.Context, source string, limit int64) ([]byte, string, error) {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, "", fmt.Errorf("generated media requires a public HTTP(S) URL")
	}
	if limit <= 0 {
		limit = 256 << 20
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, "", err
	}
	client := u.client
	if client == nil {
		client = defaultImageDownloadHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download generated media: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download generated media status %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, "", ErrAssetTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > limit {
		return nil, "", ErrAssetTooLarge
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("generated media is empty")
	}
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(data))
	isMedia := func(s string) bool {
		return strings.HasPrefix(s, "image/") || strings.HasPrefix(s, "audio/") || strings.HasPrefix(s, "video/")
	}
	if isMedia(detected) {
		contentType = detected
	} else if !isMedia(contentType) || strings.HasPrefix(detected, "text/") || detected == "application/json" {
		return nil, "", fmt.Errorf("generated content is not media")
	}
	return data, contentType, nil
}
