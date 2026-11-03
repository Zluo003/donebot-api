package service

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

const SeedanceNewTokenBaseURL = "https://api.newtoken.club"

var seedanceModelResolutions = map[string][]string{
	"doubao-seedance-2-0-260128":      {"480p", "720p", "1080p", "4k"},
	"doubao-seedance-2-0-fast-260128": {"480p", "720p"},
	"doubao-seedance-2-0-mini-260615": {"480p", "720p"},
	"doubao-seedance-2-5-260628":      {"480p", "720p", "1080p"},
}

// SeedanceSpecModelForAccount resolves protocol capabilities independently from
// the upstream endpoint name. Existing canonical mappings need no metadata.
func SeedanceSpecModelForAccount(a *Account, model string) string {
	if a != nil {
		if raw, configured := a.Credentials["seedance_model_specs"]; configured {
			if spec, found := stringMappingFromRaw(raw)[model]; found {
				if _, known := seedanceModelResolutions[spec]; known {
					return spec
				}
				return ""
			}
		}
		if upstream := a.GetMappedModel(model); upstream != "" {
			if _, known := seedanceModelResolutions[upstream]; known {
				return upstream
			}
		}
	}
	if _, known := seedanceModelResolutions[model]; known {
		return model
	}
	return ""
}

func seedanceStringMapping(value any, name string) (map[string]string, error) {
	out := map[string]string{}
	switch entries := value.(type) {
	case map[string]string:
		for key, value := range entries {
			out[key] = value
		}
	case map[string]any:
		for key, value := range entries {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%s must contain string values", name)
			}
			out[key] = text
		}
	default:
		return nil, fmt.Errorf("%s must be an object", name)
	}
	return out, nil
}

// ValidateSeedanceModelSpecs validates the complete specification map written
// by the video account panel without changing either public or upstream names.
func ValidateSeedanceModelSpecs(value, modelMapping any) (map[string]string, error) {
	specs, err := seedanceStringMapping(value, "seedance_model_specs")
	if err != nil {
		return nil, err
	}
	mapping, err := seedanceStringMapping(modelMapping, "model_mapping")
	if err != nil {
		return nil, err
	}
	if len(specs) != len(mapping) {
		return nil, fmt.Errorf("every Seedance model mapping must have exactly one official specification")
	}
	seen := make(map[string]struct{}, len(mapping))
	for model, upstream := range mapping {
		if model == "" || upstream == "" || strings.TrimSpace(model) != model || strings.TrimSpace(upstream) != upstream || strings.ContainsAny(model+upstream, "*?") {
			return nil, fmt.Errorf("seedance model mappings require non-empty concrete public and upstream names")
		}
		key := strings.ToLower(model)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("seedance public model names must be unique ignoring case")
		}
		seen[key] = struct{}{}
		if _, known := seedanceModelResolutions[specs[model]]; !known {
			return nil, fmt.Errorf("model %q requires a supported official Seedance specification", model)
		}
	}
	return specs, nil
}

// ValidateSeedanceAllowedResolutions validates the account's optional upper
// bound. An explicit empty list disables creation at every resolution.
func ValidateSeedanceAllowedResolutions(value any) ([]string, error) {
	var values []string
	switch list := value.(type) {
	case []string:
		values = list
	case []any:
		for _, entry := range list {
			str, ok := entry.(string)
			if !ok {
				return nil, fmt.Errorf("seedance_allowed_resolutions must contain strings")
			}
			values = append(values, str)
		}
	default:
		return nil, fmt.Errorf("seedance_allowed_resolutions must be an array")
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if !slices.Contains([]string{"480p", "720p", "1080p", "4k"}, value) {
			return nil, fmt.Errorf("invalid Seedance resolution %q", value)
		}
		if !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out, nil
}

func isSeedanceNewTokenAccount(a *Account) bool {
	if a == nil {
		return false
	}
	u, err := url.Parse(a.GetCredential("base_url"))
	return err == nil && strings.EqualFold(u.Hostname(), "api.newtoken.club")
}

// SeedanceAllowsResolution intersects model support with account restrictions.
// Existing Ark accounts without a list retain upstream defaults for ep-* models.
func (a *Account) SeedanceAllowsResolution(model, resolution string) bool {
	if a == nil {
		return false
	}
	spec := SeedanceSpecModelForAccount(a, model)
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	supported, known := seedanceModelResolutions[spec]
	if !known {
		_, hasSpecs := a.Credentials["seedance_model_specs"]
		if isSeedanceNewTokenAccount(a) || hasSpecs {
			return false
		}
	}
	if resolution == "" {
		if isSeedanceNewTokenAccount(a) {
			return false
		}
		resolution = "720p" // Native Ark defaults without rewriting the request.
	}
	if known && !slices.Contains(supported, resolution) {
		return false
	}
	if !known && !slices.Contains([]string{"480p", "720p", "1080p", "4k"}, resolution) {
		return false
	}
	if value, configured := a.Credentials["seedance_allowed_resolutions"]; configured {
		allowed, err := ValidateSeedanceAllowedResolutions(value)
		return err == nil && slices.Contains(allowed, resolution)
	}
	return true
}
