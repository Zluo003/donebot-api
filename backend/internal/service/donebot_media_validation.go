package service

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

func DecodeDoneBotMediaParameters(body []byte) (map[string]any, error) {
	if err := validateDoneBotNativeJSON(body, true); err != nil {
		return nil, ErrDoneBotMediaInvalid
	}
	var parameters map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&parameters); err != nil || parameters == nil {
		return nil, ErrDoneBotMediaInvalid
	}
	return parameters, nil
}

func (s *DoneBotCatalogService) ValidateMedia(ctx context.Context, owner DoneBotMediaOwner, kind, modelID string, parameters map[string]any) error {
	_, err := s.validatedMediaModel(ctx, owner, kind, modelID, "", parameters)
	return err
}

// ValidateMediaPath validates the actual native operation, including whether the
// catalog adapter advertises that submit path.
func (s *DoneBotCatalogService) ValidateMediaPath(ctx context.Context, owner DoneBotMediaOwner, kind, modelID, path string, parameters map[string]any) error {
	if path == "" {
		return ErrDoneBotMediaInvalid
	}
	_, err := s.validatedMediaModel(ctx, owner, kind, modelID, path, parameters)
	return err
}

func (s *DoneBotCatalogService) validatedMediaModel(ctx context.Context, owner DoneBotMediaOwner, kind, modelID, path string, parameters map[string]any) (*DoneBotCatalogModel, error) {
	install, err := s.installs.InstallationForUser(ctx, owner.UserID, owner.InstallationID)
	if err != nil {
		return nil, err
	}
	if install.APIKeyID != owner.APIKeyID || install.GroupID != owner.GroupID {
		return nil, ErrDoneBotMediaInvalid
	}
	catalog, err := s.Models(ctx, owner.UserID, owner.InstallationID)
	if err != nil {
		return nil, err
	}
	for i := range catalog.Models {
		model := &catalog.Models[i]
		if model.ID != modelID {
			continue
		}
		if err := validateDoneBotMediaModel(model, kind, path, parameters); err != nil {
			return nil, err
		}
		return model, nil
	}
	return nil, ErrDoneBotMediaInvalid
}

func validateDoneBotMediaModel(model *DoneBotCatalogModel, kind, path string, parameters map[string]any) error {
	if parameters == nil || model.MediaParameters == nil || model.MediaParameters.Protocol == nil {
		return ErrDoneBotMediaInvalid
	}
	media := model.MediaParameters
	protocol := media.Protocol
	known := doneBotMediaProtocol(protocol.Adapter)
	if known == nil || !reflect.DeepEqual(known, protocol) || path != "" && path != protocol.Submit.Path {
		return ErrDoneBotMediaInvalid
	}
	mediaType, iface, adapters := "", "", []string{}
	switch kind {
	case "image":
		mediaType, iface, adapters = "image", "openai.images", []string{"openai.images.v1", "apimart.images.v1"}
	case "video":
		mediaType, iface, adapters = "video", "openai.videos", []string{"seedance.videos.v1"}
	case "music":
		mediaType, iface, adapters = "music", "openai.music", []string{"apimart.music.v1"}
	case "speech":
		mediaType, iface, adapters = "audio", "openai.audio.speech", []string{"apimart.speech.v1"}
	default:
		return ErrDoneBotMediaInvalid
	}
	if !slices.Contains(model.MediaTypes, mediaType) || !slices.Contains(model.Interfaces, iface) || !slices.Contains(adapters, protocol.Adapter) {
		return ErrDoneBotMediaInvalid
	}
	if selected, exists := parameters["model"]; exists && selected != model.ID {
		return ErrDoneBotMediaInvalid
	}
	controlFields := map[string][]string{
		"openai.images.v1":   {"size", "response_format", "output_format"},
		"apimart.images.v1":  {"size", "resolution", "output_format"},
		"seedance.videos.v1": {"resolution", "ratio", "duration"},
		"apimart.music.v1":   {"version", "audio_format", "duration", "custom", "max_mode", "instrumental"},
		"apimart.speech.v1":  {"voice", "response_format"},
	}
	for _, field := range []string{"size", "resolution", "ratio", "aspect_ratio", "duration", "voice", "response_format", "output_format", "audio_format", "version", "custom", "max_mode", "instrumental"} {
		if _, exists := parameters[field]; exists && !slices.Contains(controlFields[protocol.Adapter], field) {
			return ErrDoneBotMediaInvalid
		}
	}
	for _, field := range []struct {
		name    string
		choices []string
	}{
		{"resolution", media.Resolutions}, {"ratio", media.AspectRatios}, {"aspect_ratio", media.AspectRatios},
		{"voice", media.Voices}, {"response_format", media.Formats}, {"output_format", media.Formats}, {"audio_format", media.Formats}, {"version", media.MusicVersions},
	} {
		if value, exists := parameters[field.name]; exists {
			selected, ok := value.(string)
			if !ok || !slices.Contains(field.choices, selected) {
				return ErrDoneBotMediaInvalid
			}
		}
	}
	if value, exists := parameters["size"]; exists {
		selected, ok := value.(string)
		if !ok || !slices.Contains(media.Sizes, selected) && !slices.Contains(media.Resolutions, selected) && !slices.Contains(media.AspectRatios, selected) {
			return ErrDoneBotMediaInvalid
		}
	}
	for _, field := range []string{"custom", "max_mode", "layer_decomposition", "instrumental"} {
		if value, exists := parameters[field]; exists {
			if _, ok := value.(bool); !ok {
				return ErrDoneBotMediaInvalid
			}
		}
	}
	if _, exists := parameters["seconds"]; exists {
		return ErrDoneBotMediaInvalid
	}
	if value, exists := parameters["duration"]; exists {
		seconds, ok := doneBotMediaInteger(value)
		duration := media.DurationSeconds
		if !ok || duration == nil || duration.RequiresCustom && parameters["custom"] != true {
			return ErrDoneBotMediaInvalid
		}
		if seconds != -1 || !duration.Automatic {
			if seconds < duration.Min || seconds > duration.Max || duration.Step <= 0 || (seconds-duration.Min)%duration.Step != 0 {
				return ErrDoneBotMediaInvalid
			}
		}
	}
	stringValue := func(name string) string { value, _ := parameters[name].(string); return value }
	switch protocol.Adapter {
	case "apimart.images.v1":
		size := stringValue("size")
		if size == "" && !slices.Contains(media.Sizes, "auto") {
			return ErrDoneBotMediaInvalid
		}
		resolution := stringValue("resolution")
		if slices.Contains([]string{"1K", "1.5K", "2K"}, size) {
			resolution = size
		}
		if resolution == "" {
			resolution = "1K"
			if parameters["layer_decomposition"] == true {
				resolution = "2K"
			}
		}
		if !slices.Contains(media.Resolutions, resolution) {
			return ErrDoneBotMediaInvalid
		}
	case "seedance.videos.v1":
		resolution := stringValue("resolution")
		if resolution == "" && !slices.Contains(media.Resolutions, "720p") {
			return ErrDoneBotMediaInvalid
		}
		if stringValue("ratio") == "" {
			return ErrDoneBotMediaInvalid
		}
		if _, exists := parameters["duration"]; !exists {
			return ErrDoneBotMediaInvalid
		}
	case "apimart.music.v1":
		if stringValue("version") == "" || stringValue("audio_format") == "" {
			return ErrDoneBotMediaInvalid
		}
		if _, exists := parameters["custom_model_id"]; exists {
			return ErrDoneBotMediaInvalid
		}
		if parameters["custom"] == true {
			if _, exists := parameters["duration"]; !exists {
				return ErrDoneBotMediaInvalid
			}
		}
		if parameters["max_mode"] == true && parameters["custom"] != true {
			return ErrDoneBotMediaInvalid
		}
	case "apimart.speech.v1":
		if stringValue("voice") == "" || stringValue("response_format") == "" {
			return ErrDoneBotMediaInvalid
		}
	}
	return nil
}

func doneBotMediaInteger(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case int64:
		v := int(value)
		return v, int64(v) == value
	case json.Number:
		v, err := value.Int64()
		if err != nil {
			return 0, false
		}
		i := int(v)
		return i, int64(i) == v
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value >= float64(math.MaxInt) || value < float64(math.MinInt) {
			return 0, false
		}
		v := int(value)
		return v, float64(v) == value
	case string:
		v, err := strconv.Atoi(value)
		return v, err == nil
	}
	return 0, false
}

// EstimateMedia returns a CNY cent estimate from current catalog prices and
// explicit native request units. Nil means the request has no certain estimate;
// settlement still uses the provider's measured result.
func (s *DoneBotCatalogService) EstimateMedia(ctx context.Context, owner DoneBotMediaOwner, kind, modelID string, parameters map[string]any) (*json.Number, error) {
	model, err := s.validatedMediaModel(ctx, owner, kind, modelID, "", parameters)
	if err != nil {
		return nil, err
	}
	if rates := s.installs.keys.userGroupRateRepo; rates != nil {
		override, err := rates.GetByUserAndGroup(ctx, owner.UserID, owner.GroupID)
		if err != nil {
			return nil, err
		}
		if override != nil {
			return nil, nil
		}
	}
	return estimateDoneBotMediaModel(model, parameters), nil
}

func estimateDoneBotMediaModel(model *DoneBotCatalogModel, parameters map[string]any) *json.Number {
	if model.Pricing.Currency != "CNY" {
		return nil
	}
	var tariff *float64
	units := decimal.NewFromInt(1)
	selected := func(name string) string { value, _ := parameters[name].(string); return value }
	valueAt := func(tiers map[string]float64, key string) *float64 {
		value, exists := tiers[key]
		if !exists {
			return nil
		}
		return doneBotCatalogPrice(&value, 1)
	}
	switch model.MediaParameters.Protocol.Adapter {
	case "apimart.images.v1":
		if parameters["layer_decomposition"] == true {
			return nil
		}
		tier, err := apimartImageTier(selected("size"), selected("resolution"), false)
		if err != nil {
			return nil
		}
		tariff = valueAt(model.Pricing.ImageSizes, tier)
		if count, exists := parameters["n"]; exists {
			n, ok := doneBotMediaInteger(count)
			if !ok || n != 1 {
				return nil
			}
		}
	case "apimart.music.v1":
		tariff = valueAt(model.Pricing.Tiers, selected("version"))
		if tariff == nil {
			tariff = doneBotCatalogPrice(model.Pricing.PerRequest, 1)
		}
		if parameters["max_mode"] == true {
			units = decimal.NewFromInt(2)
		}
	case "apimart.speech.v1":
		input, ok := parameters["input"].(string)
		if !ok || !utf8.ValidString(input) || utf8.RuneCountInString(input) == 0 || utf8.RuneCountInString(input) > 4096 || model.Pricing.Unit != "million_characters" {
			return nil
		}
		tariff = valueAt(model.Pricing.Tiers, "tts")
		if tariff == nil {
			tariff = doneBotCatalogPrice(model.Pricing.PerRequest, 1)
		}
		units = decimal.NewFromInt(int64(utf8.RuneCountInString(input))).Div(decimal.NewFromInt(1000000))
	case "seedance.videos.v1":
		seconds, ok := doneBotMediaInteger(parameters["duration"])
		if !ok || seconds <= 0 || model.Pricing.Unit != "second" {
			return nil
		}
		resolution := selected("resolution")
		if resolution == "" {
			resolution = "720p"
		}
		reference := false
		content, exists := parameters["content"].([]any)
		if !exists {
			return nil
		}
		for _, item := range content {
			part, ok := item.(map[string]any)
			if !ok {
				return nil
			}
			if part["type"] == "video_url" {
				reference = true
			}
		}
		tariff = valueAt(model.Pricing.Tiers, SeedancePricingTier(resolution, reference))
		units = decimal.NewFromInt(int64(seconds))
	default:
		return nil
	}
	if tariff == nil {
		return nil
	}
	cents := decimal.NewFromFloat(*tariff).Mul(units).Mul(decimal.NewFromInt(100))
	if cents.IsNegative() {
		return nil
	}
	number := json.Number(cents.String())
	return &number
}
