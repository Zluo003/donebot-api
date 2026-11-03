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
	model = a.GetMappedModel(model)
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	supported, known := seedanceModelResolutions[model]
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
