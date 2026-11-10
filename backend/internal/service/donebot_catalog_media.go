package service

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
)

type DoneBotCatalogMediaParameters struct {
	Operations      []string                             `json:"operations,omitempty"`
	Sizes           []string                             `json:"sizes,omitempty"`
	AspectRatios    []string                             `json:"aspect_ratios,omitempty"`
	Resolutions     []string                             `json:"resolutions,omitempty"`
	Quality         []string                             `json:"quality,omitempty"`
	DurationSeconds *DoneBotCatalogDuration              `json:"duration_seconds,omitempty"`
	Voices          []string                             `json:"voices,omitempty"`
	Formats         []string                             `json:"formats,omitempty"`
	MusicVersions   []string                             `json:"music_versions,omitempty"`
	IntentOptions   map[string]DoneBotCatalogMediaOption `json:"intent_options,omitempty"`
	Protocol        *DoneBotCatalogMediaProtocol         `json:"protocol,omitempty"`
}

type DoneBotCatalogMediaOption struct {
	Type        string   `json:"type"`
	NativeField string   `json:"native_field"`
	Values      []string `json:"values,omitempty"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
	Step        int      `json:"step,omitempty"`
	Automatic   *int     `json:"automatic_value,omitempty"`
	Default     any      `json:"default,omitempty"`
}

type DoneBotCatalogDuration struct {
	Min            int  `json:"min"`
	Max            int  `json:"max"`
	Step           int  `json:"step"`
	Automatic      bool `json:"automatic,omitempty"`
	RequiresCustom bool `json:"requires_custom,omitempty"`
}

// DoneBotCatalogMediaProtocol identifies a client adapter from a fixed allowlist.
// Paths describe that adapter's contract; clients must not execute arbitrary
// paths or JSON selectors received from a catalog.
type DoneBotCatalogMediaProtocol struct {
	Adapter    string                       `json:"adapter"`
	Submit     DoneBotCatalogMediaEndpoint  `json:"submit"`
	TaskIDPath string                       `json:"task_id_path,omitempty"`
	Poll       *DoneBotCatalogMediaPoll     `json:"poll,omitempty"`
	Content    *DoneBotCatalogMediaEndpoint `json:"content,omitempty"`
}

type DoneBotCatalogMediaEndpoint struct {
	Method       string `json:"method"`
	Path         string `json:"path"`
	ContentType  string `json:"content_type,omitempty"`
	ResponseKind string `json:"response_kind"`
}

type DoneBotCatalogMediaPoll struct {
	DoneBotCatalogMediaEndpoint
	StatusPaths       []string `json:"status_paths"`
	CompletedStatuses []string `json:"completed_statuses"`
	FailedStatuses    []string `json:"failed_statuses"`
	ResultPaths       []string `json:"result_paths"`
}

func doneBotMediaProtocol(adapter string) *DoneBotCatalogMediaProtocol {
	protocol := &DoneBotCatalogMediaProtocol{Adapter: adapter, Submit: DoneBotCatalogMediaEndpoint{Method: "POST", ContentType: "application/json", ResponseKind: "json"}}
	switch adapter {
	case "openai.images.v1":
		protocol.Submit.Path = "/v1/images/generations"
	case "donebot.images.async.v1":
		protocol.Submit.Path, protocol.TaskIDPath = "/v1/images/generations/async", "id"
		protocol.Poll = &DoneBotCatalogMediaPoll{DoneBotCatalogMediaEndpoint: DoneBotCatalogMediaEndpoint{Method: "GET", Path: "/v1/images/tasks/{task_id}", ResponseKind: "json"}, StatusPaths: []string{"status"}, CompletedStatuses: []string{"completed"}, FailedStatuses: []string{"failed", "uncertain"}, ResultPaths: []string{"result.data"}}
	case "apimart.images.v1", "apimart.music.v1":
		protocol.Submit.Path, protocol.TaskIDPath = "/v1/images/generations", "data.0.task_id"
		protocol.Poll = &DoneBotCatalogMediaPoll{DoneBotCatalogMediaEndpoint: DoneBotCatalogMediaEndpoint{Method: "GET", Path: "/v1/tasks/{task_id}", ResponseKind: "json"}, StatusPaths: []string{"data.status", "status"}, CompletedStatuses: []string{"completed", "success"}, FailedStatuses: []string{"failed", "cancelled"}, ResultPaths: []string{"data.result.images", "result.images"}}
		if adapter == "apimart.music.v1" {
			protocol.Submit.Path, protocol.Poll.Path = "/v1/music/generations", "/v1/music/tasks/{task_id}"
			protocol.Poll.CompletedStatuses = []string{"completed"}
			protocol.Poll.ResultPaths = []string{"data.result.music", "result.music"}
		}
	case "apimart.speech.v1":
		protocol.Submit.Path, protocol.Submit.ResponseKind = "/v1/audio/speech", "binary"
	case "seedance.videos.v1":
		protocol.Submit.Path, protocol.TaskIDPath = "/v1/videos", "id"
		protocol.Poll = &DoneBotCatalogMediaPoll{DoneBotCatalogMediaEndpoint: DoneBotCatalogMediaEndpoint{Method: "GET", Path: "/v1/videos/{task_id}", ResponseKind: "json"}, StatusPaths: []string{"status"}, CompletedStatuses: []string{"succeeded"}, FailedStatuses: []string{"failed", "cancelled", "expired"}, ResultPaths: []string{"content.video_url"}}
		protocol.Content = &DoneBotCatalogMediaEndpoint{Method: "GET", Path: "/v1/videos/{task_id}/content", ResponseKind: "binary"}
	default:
		return nil
	}
	return protocol
}

func validateDoneBotMediaMetadata(media *DoneBotCatalogMediaParameters) error {
	if media == nil {
		return nil
	}
	for _, values := range [][]string{media.Operations, media.Sizes, media.AspectRatios, media.Resolutions, media.Quality, media.Voices, media.Formats, media.MusicVersions} {
		if len(values) > 64 {
			return fmt.Errorf("too many media choices")
		}
		for i, value := range values {
			if value == "" || strings.TrimSpace(value) != value || len(value) > 64 || strings.ContainsAny(value, "/\\{}\r\n\t") || slices.Contains(values[:i], value) {
				return fmt.Errorf("invalid media choice")
			}
		}
	}
	for _, operation := range media.Operations {
		if !slices.Contains([]string{"generate", "edit", "extend"}, operation) {
			return fmt.Errorf("unknown media operation")
		}
	}
	for name, option := range media.IntentOptions {
		if !doneBotMediaIntentOptionName(name) || option.NativeField == "" || strings.TrimSpace(option.NativeField) != option.NativeField {
			return fmt.Errorf("invalid media intent option")
		}
		expected, ok := doneBotMediaIntentNativeField(media.ProtocolAdapter(), name)
		if media.Protocol == nil {
			ok = doneBotMediaNativeFieldAllowed(name, option.NativeField)
			expected = option.NativeField
		}
		if !ok || option.NativeField != expected {
			return fmt.Errorf("media intent option has an unsafe native field")
		}
		switch option.Type {
		case "enum":
			if len(option.Values) == 0 || option.Min != nil || option.Max != nil || option.Step != 0 || option.Automatic != nil {
				return fmt.Errorf("invalid enum media intent option")
			}
			for i, value := range option.Values {
				if value == "" || strings.TrimSpace(value) != value || slices.Contains(option.Values[:i], value) || !doneBotMediaOptionValueAvailable(media, name, value) {
					return fmt.Errorf("media intent option expands native choices")
				}
			}
			if option.Default != nil {
				value, ok := option.Default.(string)
				if !ok || !slices.Contains(option.Values, value) {
					return fmt.Errorf("invalid media intent option default")
				}
			}
		case "integer":
			if option.Min == nil || option.Max == nil || *option.Min < 0 || *option.Max < *option.Min || *option.Max > 3600 || option.Step < 1 || (*option.Max-*option.Min)%option.Step != 0 || len(option.Values) != 0 || option.Automatic != nil && *option.Automatic != -1 {
				return fmt.Errorf("invalid integer media intent option")
			}
			if option.Automatic != nil && media.DurationSeconds != nil && !media.DurationSeconds.Automatic {
				return fmt.Errorf("media intent option expands automatic duration")
			}
			if option.Default != nil {
				value, ok := option.Default.(float64)
				if !ok || math.Trunc(value) != value || (int(value) < *option.Min || int(value) > *option.Max || (int(value)-*option.Min)%option.Step != 0) && (option.Automatic == nil || int(value) != *option.Automatic) {
					return fmt.Errorf("invalid media intent option default")
				}
			}
		case "boolean":
			if option.Min != nil || option.Max != nil || option.Step != 0 || option.Automatic != nil || len(option.Values) != 0 {
				return fmt.Errorf("invalid boolean media intent option")
			}
			if option.Default != nil {
				if _, ok := option.Default.(bool); !ok {
					return fmt.Errorf("invalid media intent option default")
				}
			}
		default:
			return fmt.Errorf("unknown media intent option type")
		}
	}
	if duration := media.DurationSeconds; duration != nil {
		if duration.Min < 1 || duration.Max < duration.Min || duration.Max > 3600 || duration.Step < 1 || duration.Step > duration.Max-duration.Min+1 || (duration.Max-duration.Min)%duration.Step != 0 {
			return fmt.Errorf("invalid media duration range")
		}
	}
	if media.Protocol != nil {
		known := doneBotMediaProtocol(media.Protocol.Adapter)
		if known == nil || !reflect.DeepEqual(known, media.Protocol) {
			return fmt.Errorf("media protocol must match a known adapter")
		}
	}
	return nil
}

func (media *DoneBotCatalogMediaParameters) ProtocolAdapter() string {
	if media == nil || media.Protocol == nil {
		return ""
	}
	return media.Protocol.Adapter
}

func doneBotMediaIntentOptionName(name string) bool {
	return slices.Contains([]string{"size", "aspect_ratio", "resolution", "duration", "format", "quality", "version", "voice", "custom", "max_mode", "instrumental", "auto_lyrics"}, name)
}

func doneBotMediaIntentNativeField(adapter, name string) (string, bool) {
	fields := map[string]map[string]string{
		"openai.images.v1":        {"size": "size", "format": "output_format", "quality": "quality"},
		"donebot.images.async.v1": {"size": "size", "format": "output_format", "quality": "quality"},
		"apimart.images.v1":       {"size": "size", "resolution": "resolution", "format": "output_format"},
		"seedance.videos.v1":      {"aspect_ratio": "ratio", "resolution": "resolution", "duration": "duration"},
		"apimart.music.v1":        {"version": "version", "format": "audio_format", "duration": "duration", "custom": "custom", "max_mode": "max_mode", "instrumental": "instrumental", "auto_lyrics": "auto_lyrics"},
		"apimart.speech.v1":       {"voice": "voice", "format": "response_format"},
	}
	field, ok := fields[adapter][name]
	return field, ok
}

func doneBotMediaNativeFieldAllowed(name, field string) bool {
	for _, adapter := range []string{"openai.images.v1", "donebot.images.async.v1", "apimart.images.v1", "seedance.videos.v1", "apimart.music.v1", "apimart.speech.v1"} {
		if expected, ok := doneBotMediaIntentNativeField(adapter, name); ok && expected == field {
			return true
		}
	}
	return false
}

func doneBotMediaOptionValueAvailable(media *DoneBotCatalogMediaParameters, name, value string) bool {
	switch name {
	case "size":
		return len(media.Sizes) == 0 && len(media.AspectRatios) == 0 || slices.Contains(media.Sizes, value) || slices.Contains(media.AspectRatios, value)
	case "aspect_ratio":
		return len(media.AspectRatios) == 0 || slices.Contains(media.AspectRatios, value)
	case "resolution":
		return len(media.Resolutions) == 0 || slices.Contains(media.Resolutions, value)
	case "format":
		return len(media.Formats) == 0 || slices.Contains(media.Formats, value)
	case "quality":
		return len(media.Quality) == 0 || slices.Contains(media.Quality, value)
	case "version":
		return len(media.MusicVersions) == 0 || slices.Contains(media.MusicVersions, value)
	case "voice":
		return len(media.Voices) == 0 || slices.Contains(media.Voices, value)
	default:
		return false
	}
}

func doneBotMediaIntentOptions(media *DoneBotCatalogMediaParameters) map[string]DoneBotCatalogMediaOption {
	if media == nil || media.Protocol == nil {
		return nil
	}
	options := map[string]DoneBotCatalogMediaOption{}
	addEnum := func(name string, values []string, native, defaultValue string) {
		if len(values) == 0 {
			return
		}
		option := DoneBotCatalogMediaOption{Type: "enum", NativeField: native, Values: slices.Clone(values)}
		if defaultValue != "" && slices.Contains(values, defaultValue) {
			option.Default = defaultValue
		}
		options[name] = option
	}
	addBool := func(name, native string) {
		options[name] = DoneBotCatalogMediaOption{Type: "boolean", NativeField: native, Default: false}
	}
	switch media.Protocol.Adapter {
	case "openai.images.v1", "donebot.images.async.v1":
		addEnum("size", media.Sizes, "size", doneBotDefaultImageSize(media))
		addEnum("quality", media.Quality, "quality", "")
		addEnum("format", media.Formats, "output_format", firstDoneBotMediaFormat(media.Formats))
	case "apimart.images.v1":
		sizes := slices.Clone(media.Sizes)
		for _, ratio := range media.AspectRatios {
			if !slices.Contains(sizes, ratio) {
				sizes = append(sizes, ratio)
			}
		}
		addEnum("size", sizes, "size", doneBotDefaultImageSize(media))
		addEnum("resolution", media.Resolutions, "resolution", "")
		addEnum("format", media.Formats, "output_format", firstDoneBotMediaFormat(media.Formats))
	case "seedance.videos.v1":
		addEnum("aspect_ratio", media.AspectRatios, "ratio", doneBotDefaultVideoRatio(media.AspectRatios))
		addEnum("resolution", media.Resolutions, "resolution", doneBotDefaultVideoResolution(media.Resolutions))
		if duration := media.DurationSeconds; duration != nil {
			defaultValue := any(duration.Min)
			option := DoneBotCatalogMediaOption{Type: "integer", NativeField: "duration", Min: &duration.Min, Max: &duration.Max, Step: duration.Step, Default: defaultValue}
			if duration.Automatic {
				automatic := -1
				option.Automatic, option.Default = &automatic, automatic
			}
			options["duration"] = option
		}
	case "apimart.music.v1":
		addEnum("version", media.MusicVersions, "version", firstDoneBotMediaChoice(media.MusicVersions))
		addEnum("format", media.Formats, "audio_format", firstDoneBotMediaFormat(media.Formats))
		if duration := media.DurationSeconds; duration != nil {
			option := DoneBotCatalogMediaOption{Type: "integer", NativeField: "duration", Min: &duration.Min, Max: &duration.Max, Step: duration.Step}
			if !duration.RequiresCustom {
				option.Default = duration.Min
			}
			options["duration"] = option
		}
		addBool("custom", "custom")
		addBool("max_mode", "max_mode")
		addBool("instrumental", "instrumental")
		addBool("auto_lyrics", "auto_lyrics")
	case "apimart.speech.v1":
		addEnum("voice", media.Voices, "voice", firstDoneBotMediaChoice(media.Voices))
		addEnum("format", media.Formats, "response_format", firstDoneBotMediaFormat(media.Formats))
	}
	return options
}

func firstDoneBotMediaChoice(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstDoneBotMediaFormat(values []string) string {
	if slices.Contains(values, "png") {
		return "png"
	}
	if slices.Contains(values, "mp3") {
		return "mp3"
	}
	return firstDoneBotMediaChoice(values)
}

func doneBotDefaultImageSize(media *DoneBotCatalogMediaParameters) string {
	if slices.Contains(media.Sizes, "auto") {
		return "auto"
	}
	return firstDoneBotMediaChoice(media.Sizes)
}

func doneBotDefaultVideoRatio(values []string) string {
	if slices.Contains(values, "16:9") {
		return "16:9"
	}
	return firstDoneBotMediaChoice(values)
}

func doneBotDefaultVideoResolution(values []string) string {
	if slices.Contains(values, "720p") {
		return "720p"
	}
	return firstDoneBotMediaChoice(values)
}

func doneBotPublicMediaParameters(groupID int64, row DoneBotModel, accounts []Account, routes []CompositeModelRoute, configured *DoneBotCatalogMediaParameters) (*DoneBotCatalogMediaParameters, error) {
	if row.Kind == "llm" {
		if configured != nil {
			return nil, fmt.Errorf("text models cannot declare media parameters")
		}
		return nil, nil
	}
	endpoint := CompositeRouteEndpointImages
	if row.Kind == "video" || row.Kind == "audio" {
		endpoint = CompositeRouteEndpointAny
	}
	activeRoutes := []CompositeModelRoute{}
	for _, route := range routes {
		if route.Enabled && (route.GroupID == 0 || route.GroupID == groupID) {
			activeRoutes = append(activeRoutes, route)
		}
	}
	upstream := row.Model
	if row.routingModel != "" {
		upstream = row.routingModel
	} else if route, matched := matchCompositeRoute(activeRoutes, row.Model, endpoint); matched {
		if route.TargetPlatform != row.Platform {
			return nil, nil
		}
		if route.UpstreamModel != "" {
			upstream = route.UpstreamModel
		}
	}
	var common *DoneBotCatalogMediaParameters
	gifAvailable := false
	for i := range accounts {
		account := &accounts[i]
		if account.Status != StatusActive || !account.Schedulable || !slices.Contains(account.GroupIDs, groupID) || account.Platform != row.Platform || !doneBotCatalogAccountSupports(account, upstream, endpoint) {
			continue
		}
		profile := doneBotAccountMediaParameters(account, upstream, row)
		if profile == nil {
			return nil, nil
		}
		if profile.Protocol.Adapter == "donebot.images.async.v1" && slices.Contains(profile.Formats, "gif") {
			gifAvailable = true
		}
		if row.Kind == "video" {
			if len(profile.Resolutions) == 0 {
				continue
			}
			profile.Resolutions = doneBotVideoResolutionUnion(nil, profile.Resolutions)
		}
		if common == nil {
			common = profile
			continue
		}
		if common.Protocol.Adapter != profile.Protocol.Adapter {
			return nil, nil
		}
		common.Sizes = doneBotMediaIntersection(common.Sizes, profile.Sizes)
		common.AspectRatios = doneBotMediaIntersection(common.AspectRatios, profile.AspectRatios)
		if row.Kind == "video" {
			common.Resolutions = doneBotVideoResolutionUnion(common.Resolutions, profile.Resolutions)
		} else {
			common.Resolutions = doneBotMediaIntersection(common.Resolutions, profile.Resolutions)
		}
		common.Voices = doneBotMediaIntersection(common.Voices, profile.Voices)
		common.Formats = doneBotMediaIntersection(common.Formats, profile.Formats)
		common.Quality = doneBotMediaIntersection(common.Quality, profile.Quality)
		common.MusicVersions = doneBotMediaIntersection(common.MusicVersions, profile.MusicVersions)
		common.Operations = doneBotMediaIntersection(common.Operations, profile.Operations)
		common.DurationSeconds = doneBotDurationIntersection(common.DurationSeconds, profile.DurationSeconds)
	}
	if common == nil {
		if configured != nil {
			return nil, fmt.Errorf("media parameters require an active native adapter")
		}
		return nil, nil
	}
	if common.Protocol.Adapter == "apimart.images.v1" && len(common.Resolutions) == 0 {
		return nil, nil
	}
	if gifAvailable && !slices.Contains(common.Formats, "gif") {
		common.Formats = append(common.Formats, "gif")
	}
	if err := doneBotRestrictMediaParameters(common, configured); err != nil {
		return nil, err
	}
	common.IntentOptions = doneBotMediaIntentOptions(common)
	if err := doneBotRestrictMediaIntentOptions(common, configured); err != nil {
		return nil, err
	}
	if row.Kind == "video" && len(common.Resolutions) == 0 || row.Kind == "audio" && row.AudioPriceUnit == "request" && len(common.MusicVersions) == 0 {
		return nil, nil
	}
	return common, nil
}

func doneBotAccountMediaParameters(account *Account, model string, row DoneBotModel) *DoneBotCatalogMediaParameters {
	native := account.GetMappedModel(model)
	switch row.Kind {
	case "images":
		if IsAPIMartImageModel(native) && account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAPIMartImages) {
			media := &DoneBotCatalogMediaParameters{Operations: []string{"generate"}, Protocol: doneBotMediaProtocol("apimart.images.v1"), AspectRatios: []string{"1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "2:1", "1:2", "21:9"}, Formats: []string{"jpeg", "png"}}
			lowerTierPriced := doneBotCatalogPrice(row.EffectiveImagePrices["1K"], 1) != nil
			for _, resolution := range []string{"1K", "1.5K", "2K"} {
				tier := resolution
				if tier == "1.5K" {
					tier = "1K"
				}
				if lowerTierPriced && doneBotCatalogPrice(row.EffectiveImagePrices[tier], 1) != nil {
					media.Resolutions = append(media.Resolutions, resolution)
					media.Sizes = append(media.Sizes, resolution)
				}
			}
			if doneBotCatalogPrice(row.EffectiveImagePrices["1K"], 1) != nil {
				media.Sizes = append([]string{"auto"}, media.Sizes...)
			}
			return media
		}
		media := &DoneBotCatalogMediaParameters{Operations: []string{"generate", "edit"}, Protocol: doneBotMediaProtocol("donebot.images.async.v1")}
		if strings.Contains(strings.ToLower(native), "gpt-image") || strings.Contains(strings.ToLower(model), "gpt-image") || strings.Contains(strings.ToLower(row.Model), "gpt-image") {
			media.Sizes = []string{"auto", "1024x1024", "1536x1024", "1024x1536"}
			media.Quality = []string{"low", "medium", "high", "auto"}
			media.Formats = []string{"png", "jpeg", "webp"}
			if (isGPTImage26Model(native) || isGPTImage26Model(model) || isGPTImage26Model(row.Model)) && account.Type == AccountTypeAPIKey {
				media.Formats = append(media.Formats, "gif")
			}
		}
		return media
	case "video":
		if !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance) {
			return nil
		}
		spec := SeedanceSpecModelForAccount(account, model)
		resolutions, known := seedanceModelResolutions[spec]
		if !known {
			return nil
		}
		maximum := 15
		if spec == "doubao-seedance-2-5-260628" {
			maximum = 30
		}
		operations := []string{"generate"}
		if spec == "doubao-seedance-2-5-260628" {
			operations = append(operations, "edit", "extend")
		}
		media := &DoneBotCatalogMediaParameters{Operations: operations, Protocol: doneBotMediaProtocol("seedance.videos.v1"), AspectRatios: []string{"16:9", "9:16", "1:1", "21:9", "4:3", "3:4", "adaptive"}, DurationSeconds: &DoneBotCatalogDuration{Min: 4, Max: maximum, Step: 1, Automatic: true}}
		for _, resolution := range resolutions {
			if !account.SeedanceAllowsResolution(model, resolution) {
				continue
			}
			if row.EditablePricing != nil {
				for _, tier := range row.EditablePricing.VideoPrices {
					if tier.Resolution == resolution && !tier.ReferenceVideo && doneBotCatalogPrice(tier.PricePerSecond, 1) != nil {
						media.Resolutions = append(media.Resolutions, resolution)
						break
					}
				}
			}
		}
		return media
	case "audio":
		if native == "suno" && account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAPIMartSuno) {
			media := &DoneBotCatalogMediaParameters{Operations: []string{"generate"}, Protocol: doneBotMediaProtocol("apimart.music.v1"), Formats: []string{"mp3", "m4a", "wav"}, DurationSeconds: &DoneBotCatalogDuration{Min: 10, Max: 360, Step: 1, RequiresCustom: true}}
			for _, version := range []string{"v6", "v6-wild", "v6-mini"} {
				if doneBotCatalogPrice(row.EffectiveTierPrices[version], 1) != nil || doneBotCatalogPrice(row.EffectivePerRequestPrice, 1) != nil {
					media.MusicVersions = append(media.MusicVersions, version)
				}
			}
			return media
		}
		if native == "gpt-4o-mini-tts" && account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAPIMartTTS) {
			return &DoneBotCatalogMediaParameters{Operations: []string{"generate"}, Protocol: doneBotMediaProtocol("apimart.speech.v1"), Voices: []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}, Formats: []string{"opus", "aac", "flac", "wav", "pcm"}}
		}
	}
	return nil
}

func doneBotVideoResolutionUnion(left, right []string) []string {
	result := []string{}
	for _, resolution := range []string{"480p", "720p", "1080p", "4k"} {
		if slices.Contains(left, resolution) || slices.Contains(right, resolution) {
			result = append(result, resolution)
		}
	}
	return result
}

func doneBotMediaIntersection(left, right []string) []string {
	result := []string{}
	for _, value := range left {
		if slices.Contains(right, value) {
			result = append(result, value)
		}
	}
	return result
}

func doneBotDurationIntersection(left, right *DoneBotCatalogDuration) *DoneBotCatalogDuration {
	if left == nil || right == nil || left.Step != right.Step || left.Min%left.Step != right.Min%right.Step {
		return nil
	}
	result := *left
	result.Min, result.Max = max(left.Min, right.Min), min(left.Max, right.Max)
	result.Automatic, result.RequiresCustom = left.Automatic && right.Automatic, left.RequiresCustom || right.RequiresCustom
	if result.Max < result.Min {
		return nil
	}
	return &result
}

func doneBotRestrictMediaParameters(media, configured *DoneBotCatalogMediaParameters) error {
	if configured == nil {
		return nil
	}
	if err := validateDoneBotMediaMetadata(configured); err != nil {
		return err
	}
	for _, field := range []struct {
		actual     *[]string
		restricted []string
	}{{&media.Operations, configured.Operations}, {&media.Sizes, configured.Sizes}, {&media.AspectRatios, configured.AspectRatios}, {&media.Resolutions, configured.Resolutions}, {&media.Quality, configured.Quality}, {&media.Voices, configured.Voices}, {&media.Formats, configured.Formats}, {&media.MusicVersions, configured.MusicVersions}} {
		if field.restricted == nil {
			continue
		}
		for _, value := range field.restricted {
			if !slices.Contains(*field.actual, value) {
				return fmt.Errorf("media metadata cannot expand native capabilities")
			}
		}
		*field.actual = slices.Clone(field.restricted)
	}
	if requested := configured.DurationSeconds; requested != nil {
		actual := media.DurationSeconds
		if actual == nil || requested.Min < actual.Min || requested.Max > actual.Max || requested.Step%actual.Step != 0 || (requested.Min-actual.Min)%actual.Step != 0 || requested.Automatic && !actual.Automatic || actual.RequiresCustom && !requested.RequiresCustom {
			return fmt.Errorf("media metadata cannot expand native durations")
		}
		copy := *requested
		media.DurationSeconds = &copy
	}
	if configured.Protocol != nil && !reflect.DeepEqual(configured.Protocol, media.Protocol) {
		return fmt.Errorf("media metadata cannot change native adapter")
	}
	return nil
}

func doneBotRestrictMediaIntentOptions(media, configured *DoneBotCatalogMediaParameters) error {
	if configured == nil || len(configured.IntentOptions) == 0 {
		return nil
	}
	for name, restricted := range configured.IntentOptions {
		actual, ok := media.IntentOptions[name]
		if !ok || actual.Type != restricted.Type || actual.NativeField != restricted.NativeField {
			return fmt.Errorf("media intent option is not supported by the native adapter")
		}
		switch actual.Type {
		case "enum":
			for _, value := range restricted.Values {
				if !slices.Contains(actual.Values, value) {
					return fmt.Errorf("media intent option expands native choices")
				}
			}
			actual.Values = slices.Clone(restricted.Values)
			if restricted.Default != nil {
				actual.Default = restricted.Default
			} else if defaultValue, ok := actual.Default.(string); ok && !slices.Contains(actual.Values, defaultValue) {
				actual.Default = nil
			}
		case "integer":
			if restricted.Min == nil || restricted.Max == nil || actual.Min == nil || actual.Max == nil || *restricted.Min < *actual.Min || *restricted.Max > *actual.Max || restricted.Step%actual.Step != 0 || (*restricted.Min-*actual.Min)%actual.Step != 0 || restricted.Automatic != nil && actual.Automatic == nil {
				return fmt.Errorf("media intent option expands native range")
			}
			actual.Min, actual.Max, actual.Step, actual.Automatic = restricted.Min, restricted.Max, restricted.Step, restricted.Automatic
			if restricted.Default != nil {
				actual.Default = restricted.Default
			} else {
				actual.Default = nil
			}
		case "boolean":
			if restricted.Default != nil {
				actual.Default = restricted.Default
			}
		}
		media.IntentOptions[name] = actual
	}
	return nil
}
