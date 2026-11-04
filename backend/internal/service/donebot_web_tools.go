package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const doneBotWebToolsSetting = "donebot.web_tools.v1"
const doneBotWebToolsOrigin = "https://api.deepseek.com/v1/tools/"
const doneBotWebToolsMaxResponse = 16 << 20

type DoneBotWebToolsSettings struct {
	Enabled       bool `json:"enabled"`
	KeyConfigured bool `json:"key_configured"`
}

type DoneBotWebToolsSettingsInput struct {
	Enabled     bool   `json:"enabled"`
	APIKey      string `json:"api_key,omitempty"`
	ClearAPIKey bool   `json:"clear_api_key,omitempty"`
}

type doneBotWebToolsStored struct {
	Enabled bool   `json:"enabled"`
	Key     string `json:"sealed_key,omitempty"`
}

type DoneBotWebToolResult struct {
	Body            []byte
	ProviderCostCNY string
}

type doneBotWebToolWindow struct {
	start time.Time
	count int
}

type DoneBotWebToolsService struct {
	repo      SettingRepository
	encryptor SecretEncryptor
	cfg       *config.Config
	client    *http.Client
	mu        sync.Mutex
	windows   map[int64]doneBotWebToolWindow
	active    chan struct{}
}

func NewDoneBotWebToolsService(repo SettingRepository, encryptor SecretEncryptor, cfg *config.Config) *DoneBotWebToolsService {
	return &DoneBotWebToolsService{repo: repo, encryptor: encryptor, cfg: cfg,
		client:  &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		windows: map[int64]doneBotWebToolWindow{}, active: make(chan struct{}, 16)}
}

func (s *DoneBotWebToolsService) load(ctx context.Context) (doneBotWebToolsStored, error) {
	value := doneBotWebToolsStored{Enabled: true}
	if s.repo == nil {
		return value, infraerrors.ServiceUnavailable("DONEBOT_WEB_TOOLS_UNAVAILABLE", "Web tools settings are unavailable")
	}
	raw, err := s.repo.GetValue(ctx, doneBotWebToolsSetting)
	if errors.Is(err, ErrSettingNotFound) || err == nil && strings.TrimSpace(raw) == "" {
		return value, nil
	}
	if err != nil || json.Unmarshal([]byte(raw), &value) != nil {
		return value, infraerrors.ServiceUnavailable("DONEBOT_WEB_TOOLS_UNAVAILABLE", "Web tools settings are unavailable")
	}
	return value, nil
}

func (s *DoneBotWebToolsService) Get(ctx context.Context) (*DoneBotWebToolsSettings, error) {
	value, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	return &DoneBotWebToolsSettings{Enabled: value.Enabled, KeyConfigured: value.Key != ""}, nil
}

func (s *DoneBotWebToolsService) Update(ctx context.Context, input DoneBotWebToolsSettingsInput) (*DoneBotWebToolsSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(input.APIKey)
	if len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") || input.ClearAPIKey && key != "" {
		return nil, infraerrors.BadRequest("DONEBOT_WEB_TOOLS_KEY_INVALID", "Invalid web tools API key")
	}
	if input.ClearAPIKey {
		value.Key = ""
	} else if key != "" {
		if s.encryptor == nil || s.cfg == nil || !s.cfg.Totp.EncryptionKeyConfigured {
			return nil, ErrSecretEncryptionKeyNotConfigured
		}
		sealed, err := s.encryptor.Encrypt(key)
		if err != nil {
			return nil, infraerrors.ServiceUnavailable("DONEBOT_WEB_TOOLS_UNAVAILABLE", "Web tools credentials could not be saved")
		}
		value.Key = "sealed:" + sealed
	}
	value.Enabled = input.Enabled
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Set(ctx, doneBotWebToolsSetting, string(raw)); err != nil {
		return nil, infraerrors.ServiceUnavailable("DONEBOT_WEB_TOOLS_UNAVAILABLE", "Web tools settings could not be saved")
	}
	return &DoneBotWebToolsSettings{Enabled: value.Enabled, KeyConfigured: value.Key != ""}, nil
}

func decodeDoneBotWebTool(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) || validateDoneBotNativeJSON(raw, false) != nil {
		return infraerrors.BadRequest("DONEBOT_WEB_TOOLS_INPUT_INVALID", "Invalid web tool request")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return infraerrors.BadRequest("DONEBOT_WEB_TOOLS_INPUT_INVALID", "Invalid web tool request")
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil || document == nil {
		return infraerrors.BadRequest("DONEBOT_WEB_TOOLS_INPUT_INVALID", "Invalid web tool request")
	}
	for field, value := range document {
		if field != "ori_question" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return infraerrors.BadRequest("DONEBOT_WEB_TOOLS_INPUT_INVALID", "Invalid web tool request")
		}
	}
	return nil
}

func doneBotWebToolRequest(action string, raw []byte) ([]byte, error) {
	invalid := infraerrors.BadRequest("DONEBOT_WEB_TOOLS_INPUT_INVALID", "Invalid web tool request")
	validText := func(value string, max int) bool {
		return strings.TrimSpace(value) != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max
	}
	if action == "search" {
		var input struct {
			Query        string  `json:"query"`
			Count        *int    `json:"count,omitempty"`
			Original     *string `json:"ori_question,omitempty"`
			Localization string  `json:"localization,omitempty"`
			NeedContent  bool    `json:"need_content"`
		}
		if err := decodeDoneBotWebTool(raw, &input); err != nil {
			return nil, err
		}
		if !validText(input.Query, 128) || input.Count != nil && (*input.Count < 1 || *input.Count > 50) || input.Original != nil && !validText(*input.Original, 128) || input.Localization != "" && net.ParseIP(input.Localization) == nil {
			return nil, invalid
		}
		return json.Marshal(input)
	}
	if action == "fetch" {
		var input struct {
			URL string `json:"url"`
		}
		if err := decodeDoneBotWebTool(raw, &input); err != nil {
			return nil, err
		}
		parsed, err := url.Parse(input.URL)
		if !validText(input.URL, 8192) || err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, invalid
		}
		return json.Marshal(input)
	}
	return nil, invalid
}

func (s *DoneBotWebToolsService) admit(userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, window := range s.windows {
		if now.Sub(window.start) >= time.Minute {
			delete(s.windows, id)
		}
	}
	window := s.windows[userID]
	if window.count >= 60 || window.count == 0 && len(s.windows) >= 4096 {
		return false
	}
	if window.count == 0 {
		window.start = now
	}
	window.count++
	s.windows[userID] = window
	return true
}

func (s *DoneBotWebToolsService) Execute(ctx context.Context, userID int64, action string, raw []byte) (*DoneBotWebToolResult, error) {
	if ctx.Err() != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "DONEBOT_WEB_TOOLS_CANCELLED", "Web tools request was cancelled")
	}
	body, err := doneBotWebToolRequest(action, raw)
	if err != nil {
		return nil, err
	}
	settings, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled || settings.Key == "" {
		return nil, infraerrors.ServiceUnavailable("donebot_web_upstream_unavailable", "Web search and fetch are not configured")
	}
	sealed, ok := strings.CutPrefix(settings.Key, "sealed:")
	if !ok || s.encryptor == nil {
		return nil, infraerrors.ServiceUnavailable("donebot_web_upstream_unavailable", "Web tools credentials are unavailable")
	}
	key, err := s.encryptor.Decrypt(sealed)
	if err != nil || key == "" || strings.ContainsAny(key, "\r\n\x00") {
		return nil, infraerrors.ServiceUnavailable("donebot_web_upstream_unavailable", "Web tools credentials are unavailable")
	}
	if !s.admit(userID) {
		return nil, infraerrors.New(http.StatusTooManyRequests, "DONEBOT_WEB_TOOLS_RATE_LIMIT", "Web tools request limit reached")
	}
	select {
	case s.active <- struct{}{}:
		defer func() { <-s.active }()
	default:
		return nil, infraerrors.New(http.StatusTooManyRequests, "DONEBOT_WEB_TOOLS_RATE_LIMIT", "Web tools are busy")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, doneBotWebToolsOrigin+action, bytes.NewReader(body))
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("DONEBOT_WEB_TOOLS_UNAVAILABLE", "Web tools are unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, infraerrors.New(http.StatusBadGateway, "DONEBOT_WEB_TOOLS_CANCELLED", "Web tools request was cancelled")
		}
		return nil, infraerrors.New(http.StatusBadGateway, "donebot_web_upstream_unavailable", "Web tools provider request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if ctx.Err() != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "DONEBOT_WEB_TOOLS_CANCELLED", "Web tools request was cancelled")
	}
	if response.StatusCode != http.StatusOK {
		status := response.StatusCode
		if status == 401 || status == 402 || status == 408 || status == 429 || status >= 500 {
			return nil, infraerrors.New(http.StatusServiceUnavailable, "donebot_web_upstream_unavailable", "Web tools provider is unavailable")
		}
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		return nil, infraerrors.New(status, "DONEBOT_WEB_TOOLS_PROVIDER_ERROR", "Web tools provider could not complete the request")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, doneBotWebToolsMaxResponse+1))
	if ctx.Err() != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "DONEBOT_WEB_TOOLS_CANCELLED", "Web tools request was cancelled")
	}
	if err != nil || len(data) > doneBotWebToolsMaxResponse || bytes.Contains(data, []byte(key)) {
		return nil, infraerrors.New(http.StatusBadGateway, "donebot_web_upstream_unavailable", "Web tools provider returned an invalid response")
	}
	validated, nonempty, err := doneBotWebToolResponse(action, data)
	if err != nil || bytes.Contains(validated, []byte(key)) {
		return nil, infraerrors.New(http.StatusBadGateway, "donebot_web_upstream_unavailable", "Web tools provider returned an invalid response")
	}
	cost := "0"
	if nonempty {
		cost = "0.005"
	}
	return &DoneBotWebToolResult{Body: validated, ProviderCostCNY: cost}, nil
}

func doneBotWebToolResponse(action string, data []byte) ([]byte, bool, error) {
	var document map[string]json.RawMessage
	if !utf8.Valid(data) || validateDoneBotNativeJSON(data, false) != nil || json.Unmarshal(data, &document) != nil || document == nil {
		return nil, false, fmt.Errorf("invalid document")
	}
	if action == "fetch" {
		var title, content string
		if len(document["title"]) == 0 || len(document["content"]) == 0 || json.Unmarshal(document["title"], &title) != nil || json.Unmarshal(document["content"], &content) != nil || bytes.Equal(document["title"], []byte("null")) || bytes.Equal(document["content"], []byte("null")) {
			return nil, false, fmt.Errorf("invalid page")
		}
		body, err := json.Marshal(struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		}{title, content})
		return body, strings.TrimSpace(content) != "", err
	}
	var results []map[string]json.RawMessage
	if len(document["results"]) == 0 || bytes.Equal(document["results"], []byte("null")) || json.Unmarshal(document["results"], &results) != nil || len(results) > 50 {
		return nil, false, fmt.Errorf("invalid results")
	}
	clean := make([]map[string]any, 0, len(results))
	for _, result := range results {
		item := map[string]any{}
		for _, field := range []string{"title", "url", "snippet", "summary", "content"} {
			var value string
			if len(result[field]) == 0 || bytes.Equal(result[field], []byte("null")) || json.Unmarshal(result[field], &value) != nil {
				return nil, false, fmt.Errorf("invalid result field")
			}
			item[field] = value
		}
		resultURL, ok := item["url"].(string)
		if !ok {
			return nil, false, fmt.Errorf("invalid result URL")
		}
		parsed, err := url.Parse(resultURL)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, false, fmt.Errorf("invalid result URL")
		}
		for _, field := range []string{"site_name", "site_icon", "date"} {
			var value *string
			if len(result[field]) == 0 || json.Unmarshal(result[field], &value) != nil {
				return nil, false, fmt.Errorf("invalid result metadata")
			}
			item[field] = value
		}
		var score float64
		var authority int
		if len(result["rerank_score"]) == 0 || len(result["authority"]) == 0 || json.Unmarshal(result["rerank_score"], &score) != nil || json.Unmarshal(result["authority"], &authority) != nil || score < 0 || score > 3 || authority < -1 || authority > 4 || bytes.Equal(result["rerank_score"], []byte("null")) || bytes.Equal(result["authority"], []byte("null")) {
			return nil, false, fmt.Errorf("invalid result scores")
		}
		item["rerank_score"], item["authority"] = score, authority
		clean = append(clean, item)
	}
	body, err := json.Marshal(map[string]any{"results": clean})
	return body, len(clean) > 0, err
}
