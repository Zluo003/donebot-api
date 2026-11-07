package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var gptImageGIFSizePattern = regexp.MustCompile(`^([1-9][0-9]*)x([1-9][0-9]*)$`)

func isGPTImage26Model(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gpt-image-2.6-flare", "gpt-image-2.6-sunburst":
		return true
	default:
		return false
	}
}

func parseGPTImage26JSONFields(body []byte, req *OpenAIImagesRequest) error {
	if value := gjson.GetBytes(body, "complexity"); value.Exists() {
		if value.Type != gjson.String {
			return fmt.Errorf("complexity must be a string")
		}
		req.Complexity = strings.TrimSpace(value.String())
	}
	req.HasSVGOptions = gjson.GetBytes(body, "svg").Exists()
	for _, field := range []struct {
		name  string
		value **int
	}{{"frame_count", &req.FrameCount}, {"frame_duration_ms", &req.FrameDurationMS}} {
		value := gjson.GetBytes(body, field.name)
		if !value.Exists() {
			continue
		}
		number := value.Float()
		if value.Type != gjson.Number || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < 0 || number > math.MaxInt32 {
			return fmt.Errorf("%s must be a nonnegative integer", field.name)
		}
		n := int(number)
		*field.value = &n
	}
	return nil
}

func validateGPTImage26Options(req *OpenAIImagesRequest, model string, requireNativeModel bool) error {
	format := strings.ToLower(strings.TrimSpace(req.OutputFormat))
	if isGPTImage26Model(model) {
		switch req.Complexity {
		case "":
			req.Complexity = "high"
		case "low", "medium", "high":
		default:
			return fmt.Errorf("complexity must be low, medium or high")
		}
	}
	if format != "gif" {
		if req.FrameCount != nil || req.FrameDurationMS != nil {
			return fmt.Errorf("frame_count and frame_duration_ms require GIF output")
		}
		return nil
	}
	if !isGPTImage26Model(model) && (requireNativeModel || isKnownLegacyGPTImageModel(model)) {
		return fmt.Errorf("GIF output requires a GPT Image 2.6 model")
	}
	if req.Stream || req.PartialImages != nil || req.OutputCompression != nil || req.HasMask {
		return fmt.Errorf("GIF output does not support stream, partial_images, output_compression or mask")
	}
	if strings.EqualFold(req.Background, "transparent") {
		return fmt.Errorf("transparent backgrounds require PNG or WebP output")
	}
	if req.FrameCount != nil && (*req.FrameCount < 2 || *req.FrameCount > 16) {
		return fmt.Errorf("frame_count must be between 2 and 16")
	}
	if req.FrameDurationMS != nil && (*req.FrameDurationMS < 20 || *req.FrameDurationMS > 2000) {
		return fmt.Errorf("frame_duration_ms must be between 20 and 2000")
	}
	if req.Size == "" || req.Size == "auto" {
		return nil
	}
	match := gptImageGIFSizePattern.FindStringSubmatch(req.Size)
	if match == nil {
		return fmt.Errorf("GIF size must be auto or WIDTHxHEIGHT with positive multiples of 16")
	}
	for _, raw := range match[1:] {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || n%16 != 0 {
			return fmt.Errorf("GIF dimensions must be positive multiples of 16")
		}
	}
	return nil
}

// ApplyDoneBotImagePolicy enforces the DoneBot product limits independently of
// public model aliases; callers apply it again to the selected upstream model.
func (req *OpenAIImagesRequest) ApplyDoneBotImagePolicy(model string) error {
	format := strings.ToLower(strings.TrimSpace(req.OutputFormat))
	if req.HasSVGOptions || strings.Contains(format, "svg") {
		return fmt.Errorf("SVG output is not supported by DoneBot")
	}
	if req.Complexity != "" && req.Complexity != "high" {
		return fmt.Errorf("DoneBot image complexity is fixed to high")
	}
	if req.HasMask && IsGPTImageGenerationModel(model) {
		return fmt.Errorf("GPT Image mask editing is not supported by DoneBot")
	}
	if isGPTImage26Model(model) || req.usesGPTImage26() {
		req.Complexity = "high"
	}
	return nil
}

func rewriteOpenAIImagesForwardBody(body []byte, parsed *OpenAIImagesRequest, model string) ([]byte, string, error) {
	if !isGPTImage26Model(model) && !parsed.usesGPTImage26() {
		return rewriteOpenAIImagesModel(body, parsed.ContentType, model)
	}
	fields := map[string]string{"model": model, "complexity": parsed.Complexity}
	if parsed.Multipart {
		return rewriteOpenAIImagesMultipartFields(body, parsed.ContentType, fields)
	}
	var err error
	for _, name := range []string{"model", "complexity"} {
		body, err = sjson.SetBytes(body, name, fields[name])
		if err != nil {
			return nil, "", fmt.Errorf("rewrite image request %s: %w", name, err)
		}
	}
	return body, parsed.ContentType, nil
}

func validateImageJSONFieldNames(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid image request JSON: %w", err)
	}
	if token != json.Delim('{') {
		return fmt.Errorf("image request must be a JSON object")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return fmt.Errorf("invalid image request JSON: %w", err)
		}
		name, ok := token.(string)
		if !ok || seen[name] {
			return fmt.Errorf("duplicate JSON image request fields are not supported")
		}
		seen[name] = true
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return fmt.Errorf("invalid image request JSON: %w", err)
		}
	}
	if _, err = decoder.Token(); err != nil {
		return fmt.Errorf("invalid image request JSON: %w", err)
	}
	if _, err = decoder.Token(); err != io.EOF {
		return fmt.Errorf("image request must contain a single JSON object")
	}
	return nil
}

func isOpenAIImagesSingletonField(name string) bool {
	switch name {
	case "model", "prompt", "stream", "n", "size", "response_format", "background", "quality", "style", "output_format", "output_compression", "moderation", "input_fidelity", "partial_images", "complexity", "frame_count", "frame_duration_ms", "svg", "mask":
		return true
	default:
		return false
	}
}

func isKnownLegacyGPTImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	switch model {
	case "gpt-image-1", "gpt-image-1.5", "gpt-image-2", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst":
		return true
	default:
		return strings.HasPrefix(model, "gpt-image-2.5-flare-") || strings.HasPrefix(model, "gpt-image-2.5-sunburst-")
	}
}

// ValidateUpstreamImageOptions requires a known GIF-capable model on either
// side of an administrator-configured GPT Image mapping before paid dispatch.
func (req *OpenAIImagesRequest) ValidateUpstreamImageOptions(model string) error {
	if strings.EqualFold(strings.TrimSpace(req.OutputFormat), "gif") && !isGPTImage26Model(model) {
		if !req.usesGPTImage26() || !IsGPTImageGenerationModel(model) || isKnownLegacyGPTImageModel(model) {
			return fmt.Errorf("GIF output requires a GPT Image 2.6 model")
		}
	}
	return validateGPTImage26Options(req, model, false)
}

func (req *OpenAIImagesRequest) usesGPTImage26() bool {
	return isGPTImage26Model(req.requestedModel) || isGPTImage26Model(req.Model)
}
