package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SeedanceOutputStorageExtraKey = "seedance_output_storage"

func (a *Account) SeedanceOutputStorage() string {
	if a != nil {
		if value, ok := a.Extra[SeedanceOutputStorageExtraKey].(string); ok && strings.TrimSpace(strings.ToLower(value)) == "system" {
			return "system"
		}
	}
	return "upstream"
}
func ValidateSeedanceOutputStorageExtra(platform, accountType string, extra map[string]any) error {
	raw, exists := extra[SeedanceOutputStorageExtraKey]
	if !exists {
		return nil
	}
	value, ok := raw.(string)
	if !ok || (value != "system" && value != "upstream") {
		return infraerrors.BadRequest("INVALID_SEEDANCE_OUTPUT_STORAGE", "seedance_output_storage must be upstream or system")
	}
	if platform != PlatformOpenAI || accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("INVALID_SEEDANCE_ACCOUNT", "Seedance output storage requires an OpenAI API key account")
	}
	return nil
}
func AssetOwnerKeyIDFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	id, _ := ctx.Value(assetOwnerKey{}).(int64)
	return id
}
