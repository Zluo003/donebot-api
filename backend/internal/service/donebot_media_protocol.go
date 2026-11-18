package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
)

// DoneBotMediaProtocol identifies the structured downstream image/video contract.
const DoneBotMediaProtocol = "donebot.media.v1"

// UnifiedMediaRequest is independent of supplier reference and task formats.
type UnifiedMediaRequest struct {
	Protocol     string                     `json:"protocol"`
	Model        string                     `json:"model"`
	Mode         string                     `json:"mode"`
	Prompt       string                     `json:"prompt,omitempty"`
	References   []UnifiedMediaReference    `json:"references,omitempty"`
	Duration     *int                       `json:"duration,omitempty"`
	Resolution   string                     `json:"resolution,omitempty"`
	AspectRatio  string                     `json:"aspect_ratio,omitempty"`
	Size         string                     `json:"size,omitempty"`
	Count        *int                       `json:"count,omitempty"`
	OutputFormat string                     `json:"output_format,omitempty"`
	Options      map[string]json.RawMessage `json:"options,omitempty"`
}

// UnifiedMediaReference preserves each material's modality and explicit role.
type UnifiedMediaReference struct {
	Type string `json:"type"`
	Role string `json:"role"`
	URL  string `json:"url"`
}

func decodeUnifiedMedia(body []byte) (*UnifiedMediaRequest, error) {
	var probe map[string]json.RawMessage
	if json.Unmarshal(body, &probe) != nil || probe["protocol"] == nil {
		return nil, nil
	}
	var r UnifiedMediaRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("invalid unified media request: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("invalid unified media JSON")
	}
	if r.Protocol != DoneBotMediaProtocol {
		return nil, fmt.Errorf("unsupported media protocol")
	}
	if strings.TrimSpace(r.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if r.Mode == "" {
		return nil, fmt.Errorf("mode is required")
	}
	for _, ref := range r.References {
		u, err := url.Parse(ref.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return nil, fmt.Errorf("references require HTTPS URLs")
		}
		if !slices.Contains([]string{"image", "video", "audio"}, ref.Type) {
			return nil, fmt.Errorf("invalid reference type")
		}
	}
	return &r, nil
}

// UnifiedMediaNativeModel resolves only configured routing aliases; it does not select a model for a semantic request.
func (s *OpenAIGatewayService) UnifiedMediaNativeModel(ctx context.Context, key *APIKey, model, kind string) (string, error) {
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, key.GroupID, model)
	accounts, err := s.listSchedulableAccounts(ctx, key.GroupID, PlatformOpenAI)
	if err != nil {
		return "", err
	}
	spec := ""
	for i := range accounts {
		a := &accounts[i]
		if !a.IsModelSupported(mapping.MappedModel) {
			continue
		}
		native := a.GetMappedModel(mapping.MappedModel)
		if kind == "video" {
			if a.SupportsMuseModel(mapping.MappedModel) {
				native = "muse-video"
			} else if a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance) {
				native = SeedanceSpecModelForAccount(a, mapping.MappedModel)
			} else {
				continue
			}
		} else if IsAPIMartImageModel(native) {
			if !a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAPIMartImages) {
				continue
			}
		} else if !strings.HasPrefix(native, "gpt-image-") {
			continue
		}
		if native == "" {
			continue
		}
		if spec != "" && spec != native {
			return "", fmt.Errorf("model alias maps to incompatible media specifications")
		}
		spec = native
	}
	if spec == "" {
		return "", fmt.Errorf("no configured adapter for the unified %s model", kind)
	}
	return spec, nil
}

// ConvertUnifiedMedia converts explicit fields only. A nil request leaves legacy native payloads unchanged.
func ConvertUnifiedMedia(body []byte, kind, native string) ([]byte, string, error) {
	r, err := decodeUnifiedMedia(body)
	if err != nil || r == nil {
		return body, "", err
	}
	out := map[string]any{"model": r.Model}
	if r.Prompt != "" {
		out["prompt"] = r.Prompt
	}
	if r.OutputFormat != "" {
		out["output_format"] = r.OutputFormat
	}
	if r.Resolution != "" {
		out["resolution"] = r.Resolution
	}
	var options []string
	edit := false
	switch kind {
	case "image":
		if r.Duration != nil {
			return nil, "", fmt.Errorf("duration is not an image field")
		}
		if !slices.Contains([]string{"text", "reference", "edit", "layers"}, r.Mode) {
			return nil, "", fmt.Errorf("unsupported image mode")
		}
		if r.Mode == "text" && len(r.References) != 0 || r.Mode != "text" && len(r.References) == 0 {
			return nil, "", fmt.Errorf("references do not match image mode")
		}
		if r.Count != nil {
			if *r.Count < 1 {
				return nil, "", fmt.Errorf("count must be positive")
			}
			out["n"] = *r.Count
		}
		if r.Size != "" {
			out["size"] = r.Size
		}
		if r.AspectRatio != "" {
			if r.Size != "" {
				return nil, "", fmt.Errorf("size and aspect_ratio are mutually exclusive")
			}
			out["size"] = r.AspectRatio
		}
		urls := []string{}
		for _, ref := range r.References {
			if ref.Type != "image" || ref.Role != "reference" {
				return nil, "", fmt.Errorf("image requests require image references with role reference")
			}
			urls = append(urls, ref.URL)
		}
		options = []string{"background", "watermark", "optimize_prompt_options", "layer_decomposition_options"}
		if IsAPIMartImageModel(native) {
			if len(urls) > 0 {
				out["image_urls"] = urls
			}
			if r.Mode == "layers" {
				out["layer_decomposition"] = true
			}
		} else if strings.HasPrefix(native, "gpt-image-") {
			if r.Mode == "layers" || r.Resolution != "" || r.AspectRatio != "" {
				return nil, "", fmt.Errorf("unsupported GPT image controls")
			}
			options = []string{"quality", "background", "output_compression", "moderation", "user", "safety_identifier", "response_format", "complexity", "frame_count", "frame_duration_ms"}
			if len(urls) > 0 {
				images := []map[string]string{}
				for _, u := range urls {
					images = append(images, map[string]string{"image_url": u})
				}
				out["images"] = images
				edit = true
			}
		} else {
			return nil, "", fmt.Errorf("unsupported unified image adapter")
		}
	case "video":
		if r.Size != "" || r.Count != nil {
			return nil, "", fmt.Errorf("size/count are not video fields")
		}
		if !slices.Contains([]string{"text", "reference", "first_frame", "first_last_frame", "edit", "extend"}, r.Mode) {
			return nil, "", fmt.Errorf("unsupported video mode")
		}
		if strings.TrimSpace(r.Prompt) == "" || r.Duration == nil || *r.Duration <= 0 || r.Resolution == "" || r.AspectRatio == "" {
			return nil, "", fmt.Errorf("video requires prompt, positive duration, resolution and aspect_ratio")
		}
		out["duration"] = *r.Duration
		if err := validateUnifiedVideoRoles(r); err != nil {
			return nil, "", err
		}
		if native == "muse-video" {
			if r.Mode != "text" && r.Mode != "reference" {
				return nil, "", fmt.Errorf("this video mode is not supported by Muse")
			}
			out["aspect_ratio"] = r.AspectRatio
			refs := []string{}
			for _, ref := range r.References {
				if ref.Type != "image" {
					return nil, "", fmt.Errorf("only image references are supported by Muse")
				}
				refs = append(refs, ref.URL)
			}
			if len(refs) > 0 {
				out["reference_images"] = refs
			}
			if r.OutputFormat != "" {
				if r.OutputFormat != "mp4" {
					return nil, "", fmt.Errorf("only MP4 output is supported by Muse")
				}
				delete(out, "output_format")
			}
			options = []string{"extra", "timeout"}
		} else if strings.HasPrefix(native, "doubao-seedance-") {
			if r.Mode == "edit" && strings.HasPrefix(native, "doubao-seedance-2-5-") {
				return nil, "", fmt.Errorf("this Seedance adapter does not expose editing")
			}
			if native == seedance25Flash && r.Mode == "extend" {
				return nil, "", fmt.Errorf("extension is not exposed by Seedance Flash")
			}
			content := []map[string]any{{"type": "text", "text": r.Prompt}}
			for _, ref := range r.References {
				role := ref.Role
				if role == "reference" {
					role = "reference_" + ref.Type
				}
				key := ref.Type + "_url"
				content = append(content, map[string]any{"type": key, key: map[string]string{"url": ref.URL}, "role": role})
			}
			delete(out, "prompt")
			out["content"] = content
			out["ratio"] = r.AspectRatio
			out["generate_audio"] = true
			if native == "doubao-seedance-2-5-260628" && (r.Mode == "reference" || r.Mode == "extend") {
				out["omni_reference_task_type"] = r.Mode
			}
			if !strings.HasPrefix(native, "doubao-seedance-2-5-") && r.OutputFormat != "" {
				if r.OutputFormat != "mp4" {
					return nil, "", fmt.Errorf("only MP4 output is supported by Seedance 2.0")
				}
				delete(out, "output_format")
			}
			options = []string{"watermark", "return_last_frame", "execution_expires_after", "priority", "safety_identifier"}
		} else {
			return nil, "", fmt.Errorf("unsupported unified video adapter")
		}
	default:
		return nil, "", fmt.Errorf("unsupported unified media kind")
	}
	for k, v := range r.Options {
		if !slices.Contains(options, k) || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, "", fmt.Errorf("unsupported media option %s", k)
		}
		out[k] = v
	}
	path := ""
	if edit {
		path = "/v1/images/edits/async"
	}
	data, err := json.Marshal(out)
	return data, path, err
}

func validateUnifiedVideoRoles(r *UnifiedMediaRequest) error {
	first, last, refs, videos := 0, 0, 0, 0
	for _, ref := range r.References {
		switch ref.Role {
		case "first_frame":
			if ref.Type != "image" {
				return fmt.Errorf("first_frame must be an image")
			}
			first++
		case "last_frame":
			if ref.Type != "image" {
				return fmt.Errorf("last_frame must be an image")
			}
			last++
		case "reference":
			refs++
		default:
			return fmt.Errorf("unsupported reference role")
		}
		if ref.Type == "video" {
			videos++
		}
	}
	valid := false
	switch r.Mode {
	case "text":
		valid = len(r.References) == 0
	case "reference":
		valid = refs > 0 && first+last == 0
	case "first_frame":
		valid = first == 1 && last == 0 && refs == 0
	case "first_last_frame":
		valid = first == 1 && last == 1 && refs == 0
	case "edit", "extend":
		valid = videos > 0 && first+last == 0 && refs == len(r.References)
	}
	if !valid {
		return fmt.Errorf("reference roles do not match video mode")
	}
	return nil
}

type unifiedMediaContextKey struct{}

// WithUnifiedMediaProtocol preserves the downstream contract across durable dispatch and restart.
func WithUnifiedMediaProtocol(ctx context.Context) context.Context {
	return context.WithValue(ctx, unifiedMediaContextKey{}, true)
}

// IsUnifiedMediaProtocol reports the explicitly requested downstream contract.
func IsUnifiedMediaProtocol(ctx context.Context) bool {
	return ctx.Value(unifiedMediaContextKey{}) == true
}

func (task *DoneBotDispatch) unifiedPublicBody() []byte {
	status := "processing"
	if task.State == "completed" || task.State == "failed" || task.State == "uncertain" {
		status = task.State
	}
	value := map[string]any{"id": task.TaskID, "task_id": task.TaskID, "status": status, "receipt_id": task.ReceiptID}
	if slices.Contains([]string{"retrieving", "settling", "completed"}, task.State) {
		value["generation_status"] = "completed"
	}
	if task.State == "refunding" || task.State == "failed" {
		value["generation_status"] = "failed"
	}
	if status == "failed" || status == "uncertain" {
		value["error"] = task.publicError()
	}
	if status == "completed" && task.Kind == "image" {
		if task.Protocol == DoneBotDispatchImages {
			value["result"] = json.RawMessage(task.Response)
		} else {
			value["result"] = unifiedAPIMartImageResult(task.Response)
		}
	}
	if task.Kind == "video" {
		value["content_url"] = "/v1/videos/" + task.TaskID + "/content"
	}
	body, _ := json.Marshal(value)
	return body
}

func unifiedAPIMartImageResult(body []byte) map[string]any {
	var payload struct {
		Data struct {
			Result struct {
				Images []struct {
					URL                json.RawMessage  `json:"url"`
					Layers             []map[string]any `json:"layers"`
					Sizes              []any            `json:"sizes"`
					OutputFormats      []any            `json:"output_formats"`
					Backgrounds        []any            `json:"backgrounds"`
					LayerDecomposition bool             `json:"layer_decomposition"`
				} `json:"images"`
			} `json:"result"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &payload)
	images := []map[string]any{}
	for _, image := range payload.Data.Result.Images {
		var urls []string
		if json.Unmarshal(image.URL, &urls) != nil {
			var single string
			if json.Unmarshal(image.URL, &single) == nil && single != "" {
				urls = []string{single}
			}
		}
		for i, u := range urls {
			item := map[string]any{"url": u}
			if image.LayerDecomposition && i < len(image.Layers) {
				layer := image.Layers[i]
				if layer != nil {
					for key, values := range map[string][]any{"size": image.Sizes, "output_format": image.OutputFormats, "background": image.Backgrounds} {
						if layer[key] == nil && i < len(values) && values[i] != nil {
							layer[key] = values[i]
						}
					}
					item["layer"] = layer
				}
			}
			images = append(images, item)
		}
	}
	return map[string]any{"data": images}
}

// UnifiedAPIMartImageResult exposes the same image result envelope as synchronous Images wrapped by async tasks.
func UnifiedAPIMartImageResult(body []byte) []byte {
	result, _ := json.Marshal(unifiedAPIMartImageResult(body))
	return result
}
