package service

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

const seedream55Model = "seedream-5-5"

func parseSeedream55Request(root gjson.Result, info APIMartRequestInfo) (APIMartRequestInfo, error) {
	if err := validateAPIMartNativeFields(root, []string{"prompt", "size", "resolution", "background", "output_format", "image_urls", "n", "layer_decomposition_options", "optimize_prompt_options"}, []string{"layer_decomposition", "watermark"}); err != nil {
		return info, err
	}
	for _, field := range []string{"prompt", "size", "resolution", "background", "output_format"} {
		if value := root.Get(field); value.Exists() && value.Type != gjson.String {
			return info, fmt.Errorf("%s must be a string", field)
		}
	}
	for _, field := range []string{"stream", "tools", "sequential_image_generation"} {
		if root.Get(field).Exists() {
			return info, fmt.Errorf("seedream-5-5 does not support %s", field)
		}
	}
	if n := root.Get("n"); n.Exists() && (n.Type != gjson.Number || n.Float() != 1) {
		return info, fmt.Errorf("seedream-5-5 n must be 1")
	}
	if value := root.Get("image_urls"); value.Exists() {
		if !value.IsArray() || len(value.Array()) > 10 {
			return info, fmt.Errorf("image_urls must contain at most ten references")
		}
		for _, image := range value.Array() {
			if image.Type != gjson.String || !validAPIMartImageURL(image.String()) {
				return info, fmt.Errorf("invalid image reference")
			}
			info.Images = append(info.Images, image.String())
		}
	}
	info.InputImages = len(info.Images)
	info.Prompt = root.Get("prompt").String()
	info.LayerDecomposition = root.Get("layer_decomposition").Bool()
	if info.LayerDecomposition {
		if len(info.Images) != 1 {
			return info, fmt.Errorf("layer decomposition requires exactly one image")
		}
		if root.Get("background").Exists() {
			return info, fmt.Errorf("layer decomposition does not accept background")
		}
		info.Units = 30
	} else if strings.TrimSpace(info.Prompt) == "" {
		return info, fmt.Errorf("prompt is required")
	}
	if options := root.Get("layer_decomposition_options"); options.Exists() {
		if !info.LayerDecomposition || !options.IsObject() {
			return info, fmt.Errorf("layer_decomposition_options requires layer_decomposition=true and an object")
		}
		if count := options.Get("max_images"); count.Exists() {
			if count.Type != gjson.Number || count.Float() != float64(count.Int()) || count.Int() < 2 || count.Int() > 100 {
				return info, fmt.Errorf("max_images must be an integer within [2, 100]")
			}
			info.Units = count.Float()
		}
		if ocr := options.Get("text_recognition"); ocr.Exists() && ocr.Type != gjson.True && ocr.Type != gjson.False {
			return info, fmt.Errorf("text_recognition must be boolean")
		}
	}
	if background := root.Get("background"); background.Exists() {
		if !slices.Contains([]string{"auto", "transparent", "opaque"}, background.String()) {
			return info, fmt.Errorf("invalid background")
		}
		if background.String() == "transparent" && root.Get("output_format").String() == "jpeg" {
			return info, fmt.Errorf("transparent background requires PNG output")
		}
	}
	if format := root.Get("output_format"); format.Exists() && !slices.Contains([]string{"png", "jpeg"}, format.String()) {
		return info, fmt.Errorf("output_format must be png or jpeg")
	}
	if options := root.Get("optimize_prompt_options"); options.Exists() {
		if !options.IsObject() || options.Get("mode").Exists() && options.Get("mode").String() != "standard" {
			return info, fmt.Errorf("optimize_prompt_options.mode must be standard")
		}
	}
	tier, err := seedream55ImageTier(root.Get("size").String(), root.Get("resolution").String(), info.LayerDecomposition)
	info.Tier = tier
	return info, err
}

// APIMart Seedream 5.5 defaults to 2K and bills the lower tier through 2,610,000 pixels.
// https://docs.apimart.ai/cn/api-reference/images/seedream-5-5/generation
func seedream55ImageTier(size, resolution string, layer bool) (string, error) {
	tiers := []string{"1K", "1.5K", "2K", "auto"}
	if layer {
		if size == "" {
			size = resolution
		}
		if size == "" {
			size = "auto"
		}
		if !slices.Contains(tiers, size) {
			return "", fmt.Errorf("layer size must be 1K, 1.5K, 2K or auto")
		}
	} else {
		ratios := []string{"1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "2:1", "1:2", "21:9"}
		if slices.Contains(ratios, strings.ReplaceAll(size, "x", ":")) {
			if resolution == "" {
				resolution = "2K"
			}
			if !slices.Contains(tiers[:3], resolution) {
				return "", fmt.Errorf("resolution must be 1K, 1.5K or 2K")
			}
			size = resolution
		} else if size != "" && !slices.Contains(tiers, size) {
			if resolution != "" {
				return "", fmt.Errorf("pixel size does not accept resolution")
			}
			parts := strings.Split(size, "x")
			if len(parts) != 2 {
				return "", fmt.Errorf("invalid Seedream size")
			}
			w, e1 := strconv.ParseInt(parts[0], 10, 32)
			h, e2 := strconv.ParseInt(parts[1], 10, 32)
			area := w * h
			if e1 != nil || e2 != nil || w <= 0 || h <= 0 || area < 921600 || area > 4624220 || float64(w)/float64(h) < 1.0/16 || float64(w)/float64(h) > 16 {
				return "", fmt.Errorf("invalid Seedream pixel dimensions")
			}
			return apimartModelImageAreaTier(seedream55Model, "", area), nil
		} else if resolution != "" && !slices.Contains(tiers[:3], resolution) {
			return "", fmt.Errorf("resolution must be 1K, 1.5K or 2K")
		}
	}
	if size == "1K" || size == "1.5K" {
		return "1K", nil
	}
	return "2K", nil
}

func apimartModelImageAreaTier(upstream, model string, area int64) string {
	if upstream == seedream55Model || model == seedream55Model {
		if area > 2610000 {
			return "2K"
		}
		return "1K"
	}
	return apimartImageAreaTier(area)
}
