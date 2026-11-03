package service

import (
	"maps"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func normalizeSeedanceAccountCredentials(platform, accountType string, credentials map[string]any) error {
	value, resolutionsConfigured := credentials["seedance_allowed_resolutions"]
	specs, specsConfigured := credentials["seedance_model_specs"]
	if !resolutionsConfigured && !specsConfigured {
		return nil
	}
	if platform != PlatformOpenAI || accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("INVALID_SEEDANCE_ACCOUNT", "Seedance resolution settings require an OpenAI API key account")
	}
	if resolutionsConfigured {
		resolutions, err := ValidateSeedanceAllowedResolutions(value)
		if err != nil {
			return infraerrors.BadRequest("INVALID_SEEDANCE_RESOLUTIONS", err.Error())
		}
		credentials["seedance_allowed_resolutions"] = resolutions
	}
	if specsConfigured {
		validated, err := ValidateSeedanceModelSpecs(specs, credentials["model_mapping"])
		if err != nil {
			return infraerrors.BadRequest("INVALID_SEEDANCE_MODEL_SPECS", err.Error())
		}
		credentials["seedance_model_specs"] = validated
	}
	return nil
}

// Bulk credential writes merge JSONB keys. Validate the effective credentials
// for every target before writing, without copying any target's secrets into
// the shared patch or mutating the accounts loaded for preflight.
func normalizeBulkSeedanceAccountCredentials(accountIDs []int64, credentials map[string]any, targets map[int64]*Account) error {
	if len(credentials) == 0 {
		return nil
	}
	_, resolutionsConfigured := credentials["seedance_allowed_resolutions"]
	_, specsConfigured := credentials["seedance_model_specs"]
	for _, id := range accountIDs {
		account := targets[id]
		if account == nil {
			if resolutionsConfigured || specsConfigured {
				return ErrAccountNotFound
			}
			continue
		}
		merged := maps.Clone(account.Credentials)
		if merged == nil {
			merged = make(map[string]any, len(credentials))
		}
		maps.Copy(merged, credentials)
		if err := normalizeSeedanceAccountCredentials(account.Platform, account.Type, merged); err != nil {
			return err
		}
		if resolutionsConfigured {
			credentials["seedance_allowed_resolutions"] = merged["seedance_allowed_resolutions"]
		}
		if specsConfigured {
			credentials["seedance_model_specs"] = merged["seedance_model_specs"]
		}
	}
	return nil
}
