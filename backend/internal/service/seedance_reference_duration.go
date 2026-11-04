package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const (
	seedanceReferenceMaxBytes         = 256 << 20
	seedanceReferenceMaxMetadataBytes = 16 << 20
	seedanceReferenceMaxAssetBytes    = 1 << 20
)

// ResolveSeedanceReferenceVideoDuration measures the inputs before accepting a
// billable task. The result is a trusted snapshot; client-supplied JSON duration
// fields never participate. Native material URLs remain unchanged for forwarding.
func (s *OpenAIGatewayService) ResolveSeedanceReferenceVideoDuration(ctx context.Context, account *Account, info *GrokMediaRequestInfo) (float64, error) {
	return s.resolveSeedanceReferenceVideoDuration(ctx, account, info, defaultImageDownloadHTTPClient())
}

func (s *OpenAIGatewayService) resolveSeedanceReferenceVideoDuration(ctx context.Context, account *Account, info *GrokMediaRequestInfo, client *http.Client) (float64, error) {
	if info == nil {
		return 0, fmt.Errorf("reference video request is unavailable")
	}
	info.ReferenceVideoDurationSeconds = 0
	if len(info.InputVideoURLs) == 0 {
		if info.HasReferenceVideo {
			return 0, fmt.Errorf("reference video duration cannot be measured: no input videos")
		}
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if client == nil {
		client = defaultImageDownloadHTTPClient()
	}
	var total float64
	for i, raw := range info.InputVideoURLs {
		material, err := url.Parse(raw)
		if err != nil || material.User != nil {
			return 0, fmt.Errorf("reference video %d URL is invalid", i+1)
		}
		if material.Scheme == "asset" {
			if material.Host == "" || material.Path != "" || material.RawQuery != "" || material.Fragment != "" || validateUpstreamPathSegment("asset ID", material.Host) != nil {
				return 0, fmt.Errorf("reference video %d asset ID is invalid", i+1)
			}
			raw, err = s.seedanceReferenceAssetURL(ctx, account, material.Host)
			if err != nil {
				return 0, fmt.Errorf("reference video %d: %w", i+1, err)
			}
		}
		seconds, err := seedanceDownloadReferenceSeconds(ctx, client, raw)
		if err != nil {
			return 0, fmt.Errorf("reference video %d: %w", i+1, err)
		}
		total += seconds
		if total <= 0 || math.IsNaN(total) || math.IsInf(total, 0) {
			return 0, fmt.Errorf("reference video duration is invalid")
		}
	}
	info.ReferenceVideoDurationSeconds = total
	return total, nil
}

// The linked Seedance Character API documents POST /api/v3/asset/get with Bearer
// auth and {"id": "..."}. The selected channel must expose that API and authorize
// its configured credential for the asset; support is channel specific. Query it
// rather than treating a supplier asset ID as a gateway upload ID.
func (s *OpenAIGatewayService) seedanceReferenceAssetURL(ctx context.Context, account *Account, id string) (string, error) {
	if s == nil || s.httpUpstream == nil || account == nil {
		return "", fmt.Errorf("reference asset lookup is unavailable")
	}
	token := strings.TrimSpace(account.GetCredential("api_key"))
	if token == "" {
		return "", fmt.Errorf("reference asset account credential is unavailable")
	}
	base, err := s.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return "", fmt.Errorf("reference asset account base URL is invalid")
	}
	taskURL, err := buildSeedanceURL(base, SeedanceEndpointCreate, "")
	if err != nil {
		return "", fmt.Errorf("reference asset lookup URL is invalid")
	}
	target := strings.TrimSuffix(taskURL, "/contents/generations/tasks") + "/asset/get"
	body, _ := json.Marshal(map[string]string{"id": id})
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(ctx), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("reference asset lookup request is invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(req.Header)
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil || resp == nil || resp.Body == nil {
		return "", fmt.Errorf("reference asset lookup failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("reference asset lookup returned HTTP %d", resp.StatusCode)
	}
	response, err := io.ReadAll(io.LimitReader(resp.Body, seedanceReferenceMaxAssetBytes+1))
	if err != nil || len(response) > seedanceReferenceMaxAssetBytes || !gjson.ValidBytes(response) {
		return "", fmt.Errorf("reference asset lookup response is invalid")
	}
	if gjson.GetBytes(response, "success").Type == gjson.False {
		return "", fmt.Errorf("reference asset lookup was rejected")
	}
	asset := gjson.GetBytes(response, "result")
	if !asset.IsObject() {
		asset = gjson.GetBytes(response, "Result")
	}
	if asset.Get("Id").String() != id || !strings.EqualFold(asset.Get("AssetType").String(), "Video") || !strings.EqualFold(asset.Get("Status").String(), "Active") {
		return "", fmt.Errorf("reference asset is not an active video")
	}
	materialURL := asset.Get("URL").String()
	if !seedanceReferencePublicURL(materialURL) {
		return "", fmt.Errorf("reference asset download URL is invalid or not public")
	}
	return materialURL, nil
}

func seedanceReferencePublicURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") && !isBlockedImageDownloadHost(u.Hostname())
}

func seedanceDownloadReferenceSeconds(ctx context.Context, client *http.Client, raw string) (float64, error) {
	if !seedanceReferencePublicURL(raw) {
		return 0, fmt.Errorf("download URL is invalid or not public")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return 0, fmt.Errorf("download request is invalid")
	}
	// No supplier credentials or header overrides reach a user material URL.
	// The production client validates DNS/socket addresses and every redirect.
	resp, err := client.Do(req)
	if err != nil || resp == nil || resp.Body == nil {
		return 0, fmt.Errorf("download failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > seedanceReferenceMaxBytes {
		return 0, fmt.Errorf("video is too large to measure (maximum 256 MiB)")
	}
	return seedanceReferenceMovieSeconds(resp.Body)
}

// Seedance reference videos support MP4 and MOV. Both store presentation timing
// in ISO BMFF/QuickTime movie boxes. Scan without buffering media payloads; only
// the bounded moov metadata is retained, including for files whose moov is last.
func seedanceReferenceMovieSeconds(reader io.Reader) (float64, error) {
	var scanned uint64
	for scanned+8 <= seedanceReferenceMaxBytes {
		var header [16]byte
		if _, err := io.ReadFull(reader, header[:8]); err != nil {
			return 0, fmt.Errorf("MP4/MOV video duration is unavailable")
		}
		size, headerSize := uint64(binary.BigEndian.Uint32(header[:4])), uint64(8)
		kind := string(header[4:8])
		if size == 1 {
			if _, err := io.ReadFull(reader, header[8:16]); err != nil {
				return 0, fmt.Errorf("MP4/MOV box header is truncated")
			}
			size, headerSize = binary.BigEndian.Uint64(header[8:16]), 16
		}
		if size == 0 {
			if kind != "moov" {
				return 0, fmt.Errorf("MP4/MOV video duration is unavailable")
			}
			remaining := min(uint64(seedanceReferenceMaxMetadataBytes), seedanceReferenceMaxBytes-scanned-headerSize)
			metadata, err := io.ReadAll(io.LimitReader(reader, int64(remaining)+1))
			if err != nil || uint64(len(metadata)) > remaining {
				return 0, fmt.Errorf("MP4/MOV metadata is invalid or too large")
			}
			return seedanceReferenceMoovSeconds(metadata)
		}
		if size < headerSize {
			return 0, fmt.Errorf("MP4/MOV box size is invalid")
		}
		if size > seedanceReferenceMaxBytes-scanned {
			return 0, fmt.Errorf("video is too large to measure (maximum 256 MiB)")
		}
		payloadSize := size - headerSize
		if kind == "moov" {
			if payloadSize > seedanceReferenceMaxMetadataBytes {
				return 0, fmt.Errorf("MP4/MOV metadata is too large")
			}
			metadata := make([]byte, int(payloadSize))
			if _, err := io.ReadFull(reader, metadata); err != nil {
				return 0, fmt.Errorf("MP4/MOV metadata is truncated")
			}
			return seedanceReferenceMoovSeconds(metadata)
		}
		if _, err := io.CopyN(io.Discard, reader, int64(payloadSize)); err != nil {
			return 0, fmt.Errorf("MP4/MOV video is truncated")
		}
		scanned += size
	}
	return 0, fmt.Errorf("video is too large to measure (maximum 256 MiB)")
}

func seedanceReferenceWalkBoxes(data []byte, visit func(string, []byte) error) error {
	for len(data) > 0 {
		if len(data) < 8 {
			return fmt.Errorf("MP4/MOV metadata box is truncated")
		}
		size, headerSize := uint64(binary.BigEndian.Uint32(data[:4])), uint64(8)
		switch size {
		case 1:
			if len(data) < 16 {
				return fmt.Errorf("MP4/MOV metadata box is truncated")
			}
			size, headerSize = binary.BigEndian.Uint64(data[8:16]), 16
		case 0:
			size = uint64(len(data))
		}
		if size < headerSize || size > uint64(len(data)) {
			return fmt.Errorf("MP4/MOV metadata box size is invalid")
		}
		if err := visit(string(data[4:8]), data[headerSize:size]); err != nil {
			return err
		}
		data = data[size:]
	}
	return nil
}

func seedanceReferenceTimeHeader(data []byte) (uint32, uint64, error) {
	if len(data) >= 20 && data[0] == 0 {
		return binary.BigEndian.Uint32(data[12:16]), uint64(binary.BigEndian.Uint32(data[16:20])), nil
	}
	if len(data) >= 32 && data[0] == 1 {
		return binary.BigEndian.Uint32(data[20:24]), binary.BigEndian.Uint64(data[24:32]), nil
	}
	return 0, 0, fmt.Errorf("MP4/MOV timing header is invalid")
}

func seedanceReferencePositiveSeconds(scale uint32, ticks uint64) float64 {
	if scale == 0 || ticks == 0 || ticks == math.MaxUint64 || ticks == math.MaxUint32 {
		return 0 // Zero/unknown durations cannot become free input seconds.
	}
	seconds := float64(ticks) / float64(scale)
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0
	}
	return seconds
}

func seedanceReferenceMoovSeconds(data []byte) (float64, error) {
	var scale uint32
	var tracks [][]byte
	err := seedanceReferenceWalkBoxes(data, func(kind string, payload []byte) error {
		switch kind {
		case "mvhd":
			var err error
			scale, _, err = seedanceReferenceTimeHeader(payload)
			return err
		case "trak":
			tracks = append(tracks, payload)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	var seconds float64
	for _, track := range tracks {
		var ticks uint64
		var mediaSeconds float64
		video := false
		err := seedanceReferenceWalkBoxes(track, func(kind string, payload []byte) error {
			switch kind {
			case "tkhd":
				switch {
				case len(payload) >= 24 && payload[0] == 0:
					ticks = uint64(binary.BigEndian.Uint32(payload[20:24]))
				case len(payload) >= 36 && payload[0] == 1:
					ticks = binary.BigEndian.Uint64(payload[28:36])
				default:
					return fmt.Errorf("MP4/MOV track timing is invalid")
				}
			case "mdia":
				return seedanceReferenceWalkBoxes(payload, func(kind string, payload []byte) error {
					switch kind {
					case "hdlr":
						video = len(payload) >= 12 && string(payload[8:12]) == "vide"
					case "mdhd":
						mediaScale, duration, err := seedanceReferenceTimeHeader(payload)
						if err != nil {
							return err
						}
						mediaSeconds = seedanceReferencePositiveSeconds(mediaScale, duration)
					}
					return nil
				})
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		if video {
			trackSeconds := seedanceReferencePositiveSeconds(scale, ticks)
			if trackSeconds == 0 {
				trackSeconds = mediaSeconds
			}
			seconds = math.Max(seconds, trackSeconds)
		}
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("MP4/MOV video duration is missing, zero, or unsupported")
	}
	return seconds, nil
}
