package service

import (
	"maps"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func normalizeSeedanceAccountCredentials(platform, accountType string, credentials map[string]any) error {
	value, configured := credentials["seedance_allowed_resolutions"]
	if !configured {
		return nil
	}
	if platform != PlatformOpenAI || accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("INVALID_SEEDANCE_ACCOUNT", "Seedance resolution settings require an OpenAI API key account")
	}
	resolutions, err := ValidateSeedanceAllowedResolutions(value)
	if err != nil {
		return infraerrors.BadRequest("INVALID_SEEDANCE_RESOLUTIONS", err.Error())
	}
	credentials["seedance_allowed_resolutions"] = resolutions
	return nil
}

// Bulk credential writes merge JSONB keys. Validate the effective credentials
// for every target before writing, without copying any target's secrets into
// the shared patch or mutating the accounts loaded for preflight.
func normalizeBulkSeedanceAccountCredentials(accountIDs []int64, credentials map[string]any, targets map[int64]*Account) error {
	if len(credentials) == 0 {
		return nil
	}
	_, configured := credentials["seedance_allowed_resolutions"]
	for _, id := range accountIDs {
		account := targets[id]
		if account == nil {
			if configured {
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
		if configured {
			credentials["seedance_allowed_resolutions"] = merged["seedance_allowed_resolutions"]
		}
	}
	return nil
}
