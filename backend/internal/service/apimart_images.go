package service

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var apimartImagesAdapter = apimartNativeAdapter{
	capability:     OpenAIEndpointCapabilityAPIMartImages,
	generationPath: "/images/generations",
	taskPath:       "/tasks",
	parse:          parseAPIMartImageRequest,
	supportsModel:  IsAPIMartImageModel,
	forward:        forwardAPIMartImages,
	submitted:      apimartImageSubmission,
	complete:       completeAPIMartImages,
}

func parseAPIMartImageRequest(root gjson.Result, info APIMartRequestInfo) (APIMartRequestInfo, error) {
	if err := validateAPIMartNativeFields(root, []string{"prompt", "size", "resolution", "output_format", "layer_decomposition", "nsfw_check"}, []string{"layer_decomposition", "nsfw_check", "watermark"}); err != nil {
		return info, err
	}
	if options := root.Get("optimize_prompt_options"); options.Exists() {
		if !options.IsObject() || options.Get("mode").Exists() && options.Get("mode").String() != "standard" {
			return info, fmt.Errorf("optimize_prompt_options.mode must be standard")
		}
	}
	if mode := root.Get("optimize_prompt_options\\.mode"); mode.Exists() && mode.String() != "standard" {
		return info, fmt.Errorf("optimize_prompt_options.mode must be standard")
	}
	info.Prompt = root.Get("prompt").String()
	for _, k := range []string{"stream", "tools", "sequential_image_generation"} {
		if root.Get(k).Exists() {
			return info, fmt.Errorf("%s is not supported by Seedream Flash", k)
		}
	}
	if n := root.Get("n"); n.Exists() && (n.Type != gjson.Number || n.Float() != 1) {
		return info, fmt.Errorf("seedream Flash generates one image per request")
	}
	if v := root.Get("image_urls"); v.Exists() {
		if !v.IsArray() || len(v.Array()) > 10 {
			return info, fmt.Errorf("image_urls must contain at most ten references")
		}
		for _, v := range v.Array() {
			if v.Type != gjson.String || !validAPIMartImageURL(v.String()) {
				return info, fmt.Errorf("invalid image reference")
			}
			info.Images = append(info.Images, v.String())
		}
	}
	layer := root.Get("layer_decomposition").Bool()
	info.LayerDecomposition = layer
	if layer {
		info.Units = 17
	}
	if layer && len(info.Images) != 1 {
		return info, fmt.Errorf("layer decomposition requires one image")
	}
	if !layer && strings.TrimSpace(info.Prompt) == "" {
		return info, fmt.Errorf("prompt is required")
	}
	if root.Get("background").String() == "transparent" && (len(info.Images) != 1 || root.Get("output_format").String() != "png") {
		return info, fmt.Errorf("transparent background requires one reference and PNG output")
	}
	tier, err := apimartImageTier(root.Get("size").String(), root.Get("resolution").String(), layer)
	if err != nil {
		return info, err
	}
	info.Tier = tier
	if layer && root.Get("size").String() == "auto" {
		info.Tier = "2K"
	}
	if layer && !root.Get("size").Exists() {
		info.Tier = "2K"
	}
	return info, nil
}

func forwardAPIMartImages(ctx context.Context, s *OpenAIGatewayService, c *gin.Context, a *Account, op apimartOperation, id string, body []byte) (*APIMartResponse, error) {
	return s.forwardAPIMartAsync(ctx, c, a, op, id, body)
}

func apimartImageSubmission(response *APIMartResponse) error {
	response.TaskID = gjson.GetBytes(response.Body, "data.0.task_id").String()
	if response.TaskID == "" {
		return fmt.Errorf("APIMart submission response has no task_id")
	}
	response.Result.ResponseID = "apimart:image:" + response.TaskID
	return nil
}

func completeAPIMartImages(p *APIMartPending, root gjson.Result, r *OpenAIForwardResult) bool {
	if root.Get("status").String() != "success" {
		return false
	}
	breakdown := map[string]int{}
	sizes := []string{}
	for _, v := range root.Get("result.images").Array() {
		urls := v.Get("url").Array()
		if v.Get("url").Type == gjson.String {
			urls = []gjson.Result{v.Get("url")}
		}
		for i, u := range urls {
			if strings.TrimSpace(u.String()) == "" {
				continue
			}
			size := v.Get(fmt.Sprintf("sizes.%d", i)).String()
			if size == "" {
				size = v.Get(fmt.Sprintf("layers.%d.size", i)).String()
			}
			tier := p.Tier
			if w, h, ok := parseImageBillingDimensions(size); ok && w > 0 && h > 0 {
				tier = apimartImageAreaTier(int64(w) * int64(h))
			}
			breakdown[tier]++
			sizes = append(sizes, size)
		}
	}
	count := 0
	for _, n := range breakdown {
		count += n
	}
	if count == 0 {
		return false
	}
	r.ImageCount = count
	r.ImageSize = p.Tier
	r.ImageInputSize = p.Tier
	r.ImageSizeSource = ImageSizeSourceOutput
	r.ImageSizeBreakdown = breakdown
	r.ImageOutputSizes = sizes
	r.ImageEndpoint = true
	return true
}

func validAPIMartImageURL(raw string) bool {
	if strings.HasPrefix(raw, "data:image/") && strings.Contains(raw, ";base64,") {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.User == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http")
}

// APIMart bills Seedream outputs by pixel area; 1K and 1.5K share the lower tier.
// https://docs.apimart.ai/cn/api-reference/images/seedream-5-0-flash/generation
func apimartImageAreaTier(area int64) string {
	if area > 2601124 {
		return "2K"
	}
	return "1K"
}

func apimartImageTier(size, resolution string, layer bool) (string, error) {
	size = strings.TrimSpace(size)
	res := strings.ToUpper(resolution)
	if size == "" {
		size = "auto"
	}
	if slices.Contains([]string{"1K", "1.5K", "2K"}, strings.ToUpper(size)) {
		res = strings.ToUpper(size)
	} else if size != "auto" {
		ratios := []string{"1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "2:1", "1:2", "21:9", "1x1", "4x3", "3x4", "16x9", "9x16", "3x2", "2x3", "2x1", "1x2", "21x9"}
		if layer {
			return "", fmt.Errorf("layer decomposition accepts only auto or a size tier")
		}
		if !slices.Contains(ratios, size) {
			normalized := strings.ReplaceAll(strings.ReplaceAll(size, "X", "x"), "×", "x")
			parts := strings.Split(normalized, "x")
			if len(parts) != 2 {
				return "", fmt.Errorf("invalid Seedream size")
			}
			w, e1 := strconv.ParseInt(parts[0], 10, 32)
			h, e2 := strconv.ParseInt(parts[1], 10, 32)
			area := w * h
			if e1 != nil || e2 != nil || w <= 0 || h <= 0 || area < 921600 || area > 4624220 || float64(w)/float64(h) < 1.0/16 || float64(w)/float64(h) > 16 {
				return "", fmt.Errorf("invalid Seedream pixel dimensions")
			}
			return apimartImageAreaTier(area), nil
		}
	}
	if res == "" {
		res = "1K"
	}
	if !slices.Contains([]string{"1K", "1.5K", "2K"}, res) {
		return "", fmt.Errorf("resolution must be 1K, 1.5K, or 2K")
	}
	if res == "1.5K" {
		res = "1K"
	}
	return res, nil
}
