package service

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var apimartMusicAdapter = apimartNativeAdapter{
	capability:     OpenAIEndpointCapabilityAPIMartSuno,
	generationPath: "/music/generations",
	taskPath:       "/music/tasks",
	defaultModel:   "suno",
	parse:          parseAPIMartMusicRequest,
	supportsModel:  func(model string) bool { return model == "suno" },
	forward:        forwardAPIMartMusic,
	submitted:      apimartMusicSubmission,
	complete:       completeAPIMartMusic,
}

func parseAPIMartMusicRequest(root gjson.Result, info APIMartRequestInfo) (APIMartRequestInfo, error) {
	if err := validateAPIMartNativeFields(root, []string{"version", "custom_model_id", "prompt", "max_mode", "custom", "instrumental"}, []string{"max_mode", "custom", "instrumental", "auto_lyrics"}); err != nil {
		return info, err
	}
	custom, inst := root.Get("custom").Bool(), root.Get("instrumental").Bool()
	version, id := root.Get("version").String(), root.Get("custom_model_id").String()
	if id != "" {
		if version != "" || root.Get("persona_id").Exists() {
			return info, fmt.Errorf("custom_model_id cannot be combined with version or persona_id")
		}
		info.Tier = "custom-model"
	} else {
		if !slices.Contains([]string{"v6", "v6-wild", "v6-mini"}, version) {
			return info, fmt.Errorf("version must be v6, v6-wild, or v6-mini")
		}
		info.Tier = version
	}
	info.Prompt = root.Get("prompt").String()
	limit := 3000
	if custom {
		limit = 5000
	}
	if (!custom || !inst) && strings.TrimSpace(info.Prompt) == "" {
		return info, fmt.Errorf("prompt is required")
	}
	if utf8.RuneCountInString(info.Prompt) > limit {
		return info, fmt.Errorf("prompt exceeds %d characters", limit)
	}
	if custom && (utf8.RuneCountInString(root.Get("title").String()) > 80 || utf8.RuneCountInString(root.Get("style").String()) > 1000) {
		return info, fmt.Errorf("title or style exceeds the native length limit")
	}
	if root.Get("max_mode").Bool() {
		if !custom {
			return info, fmt.Errorf("max_mode requires custom mode")
		}
		info.Units = 2
	}
	for _, k := range []string{"style_weight", "weirdness_constraint", "audio_weight"} {
		if v := root.Get(k); v.Exists() && (v.Type != gjson.Number || v.Float() < 0 || v.Float() > 1) {
			return info, fmt.Errorf("%s must be between zero and one", k)
		}
	}
	if v := root.Get("duration"); v.Exists() && (!custom || v.Type != gjson.Number || math.Trunc(v.Float()) != v.Float() || v.Int() < 10 || v.Int() > 360) {
		return info, fmt.Errorf("duration requires custom mode and an integer between 10 and 360")
	}
	if v := root.Get("variety"); v.Exists() && !slices.Contains([]string{"off", "normal", "high", "extra", "max"}, v.String()) {
		return info, fmt.Errorf("invalid variety")
	}
	if v := root.Get("audio_format"); v.Exists() && !slices.Contains([]string{"mp3", "m4a", "wav"}, v.String()) {
		return info, fmt.Errorf("invalid audio_format")
	}
	return info, nil
}

func forwardAPIMartMusic(ctx context.Context, s *OpenAIGatewayService, c *gin.Context, a *Account, op apimartOperation, id string, body []byte) (*APIMartResponse, error) {
	return s.forwardAPIMartAsync(ctx, c, a, op, id, body)
}

func apimartMusicSubmission(response *APIMartResponse) error {
	response.TaskID = gjson.GetBytes(response.Body, "data.0.task_id").String()
	if response.TaskID == "" {
		return fmt.Errorf("APIMart submission response has no task_id")
	}
	response.Result.ResponseID = "apimart:music:" + response.TaskID
	return nil
}

func completeAPIMartMusic(p *APIMartPending, root gjson.Result, r *OpenAIForwardResult) bool {
	if root.Get("status").String() != "completed" {
		return false
	}
	found := false
	for _, v := range root.Get("result.music").Array() {
		if v.Get("audio_url").String() != "" {
			found = true
		}
	}
	if !found {
		return false
	}
	r.AudioUsage = &AudioUsage{Mode: p.Tier, DurationOrUnits: p.Units}
	return true
}
