package service

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

type DoneBotCatalogMediaParameters struct {
	Sizes           []string                     `json:"sizes,omitempty"`
	AspectRatios    []string                     `json:"aspect_ratios,omitempty"`
	Resolutions     []string                     `json:"resolutions,omitempty"`
	DurationSeconds *DoneBotCatalogDuration      `json:"duration_seconds,omitempty"`
	Voices          []string                     `json:"voices,omitempty"`
	Formats         []string                     `json:"formats,omitempty"`
	MusicVersions   []string                     `json:"music_versions,omitempty"`
	Protocol        *DoneBotCatalogMediaProtocol `json:"protocol,omitempty"`
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
	case "apimart.images.v1", "apimart.music.v1":
		protocol.Submit.Path, protocol.TaskIDPath = "/v1/images/generations", "data.0.task_id"
		protocol.Poll = &DoneBotCatalogMediaPoll{DoneBotCatalogMediaEndpoint: DoneBotCatalogMediaEndpoint{Method: "GET", Path: "/v1/tasks/{task_id}", ResponseKind: "json"}, StatusPaths: []string{"data.status", "status"}, CompletedStatuses: []string{"success"}, FailedStatuses: []string{"failed", "cancelled"}, ResultPaths: []string{"data.result.images", "result.images"}}
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
	for _, values := range [][]string{media.Sizes, media.AspectRatios, media.Resolutions, media.Voices, media.Formats, media.MusicVersions} {
		if len(values) > 64 {
			return fmt.Errorf("too many media choices")
		}
		for i, value := range values {
			if value == "" || strings.TrimSpace(value) != value || len(value) > 64 || strings.ContainsAny(value, "/\\{}\r\n\t") || slices.Contains(values[:i], value) {
				return fmt.Errorf("invalid media choice")
			}
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
	if route, matched := matchCompositeRoute(activeRoutes, row.Model, endpoint); matched {
		if route.TargetPlatform != row.Platform {
			return nil, nil
		}
		if route.UpstreamModel != "" {
			upstream = route.UpstreamModel
		}
	}
	var common *DoneBotCatalogMediaParameters
	for i := range accounts {
		account := &accounts[i]
		if account.Status != StatusActive || !account.Schedulable || !slices.Contains(account.GroupIDs, groupID) || account.Platform != row.Platform || !doneBotCatalogAccountSupports(account, upstream, endpoint) {
			continue
		}
		profile := doneBotAccountMediaParameters(account, upstream, row)
		if profile == nil {
			return nil, nil
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
		common.Resolutions = doneBotMediaIntersection(common.Resolutions, profile.Resolutions)
		common.Voices = doneBotMediaIntersection(common.Voices, profile.Voices)
		common.Formats = doneBotMediaIntersection(common.Formats, profile.Formats)
		common.MusicVersions = doneBotMediaIntersection(common.MusicVersions, profile.MusicVersions)
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
	if err := doneBotRestrictMediaParameters(common, configured); err != nil {
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
			media := &DoneBotCatalogMediaParameters{Protocol: doneBotMediaProtocol("apimart.images.v1"), AspectRatios: []string{"1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "2:1", "1:2", "21:9"}}
			for _, resolution := range []string{"1K", "1.5K", "2K"} {
				tier := resolution
				if tier == "1.5K" {
					tier = "1K"
				}
				if doneBotCatalogPrice(row.EffectiveImagePrices[tier], 1) != nil {
					media.Resolutions = append(media.Resolutions, resolution)
					media.Sizes = append(media.Sizes, resolution)
				}
			}
			if doneBotCatalogPrice(row.EffectiveImagePrices["1K"], 1) != nil {
				media.Sizes = append([]string{"auto"}, media.Sizes...)
			}
			return media
		}
		return &DoneBotCatalogMediaParameters{Protocol: doneBotMediaProtocol("openai.images.v1")}
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
		media := &DoneBotCatalogMediaParameters{Protocol: doneBotMediaProtocol("seedance.videos.v1"), AspectRatios: []string{"16:9", "9:16", "1:1", "21:9", "4:3", "3:4", "adaptive"}, DurationSeconds: &DoneBotCatalogDuration{Min: 4, Max: maximum, Step: 1, Automatic: true}}
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
			media := &DoneBotCatalogMediaParameters{Protocol: doneBotMediaProtocol("apimart.music.v1"), Formats: []string{"mp3", "m4a", "wav"}, DurationSeconds: &DoneBotCatalogDuration{Min: 10, Max: 360, Step: 1, RequiresCustom: true}}
			for _, version := range []string{"v6", "v6-wild", "v6-mini"} {
				if doneBotCatalogPrice(row.EffectiveTierPrices[version], 1) != nil || doneBotCatalogPrice(row.EffectivePerRequestPrice, 1) != nil {
					media.MusicVersions = append(media.MusicVersions, version)
				}
			}
			return media
		}
		if native == "gpt-4o-mini-tts" && account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAPIMartTTS) {
			return &DoneBotCatalogMediaParameters{Protocol: doneBotMediaProtocol("apimart.speech.v1"), Voices: []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}, Formats: []string{"opus", "aac", "flac", "wav", "pcm"}}
		}
	}
	return nil
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
	}{{&media.Sizes, configured.Sizes}, {&media.AspectRatios, configured.AspectRatios}, {&media.Resolutions, configured.Resolutions}, {&media.Voices, configured.Voices}, {&media.Formats, configured.Formats}, {&media.MusicVersions, configured.MusicVersions}} {
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
