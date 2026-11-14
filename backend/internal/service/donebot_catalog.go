package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/tidwall/gjson"
)

const doneBotCapabilitiesSetting = "donebot.model_capabilities.v4"

var (
	ErrDoneBotCatalogCurrency      = infraerrors.ServiceUnavailable("DONEBOT_CATALOG_CURRENCY", "DoneBot catalog currency must be CNY")
	ErrDoneBotCatalogCapabilities  = infraerrors.ServiceUnavailable("DONEBOT_CATALOG_CAPABILITIES", "DoneBot model capabilities configuration is invalid")
	ErrDoneBotReasoningUnsupported = infraerrors.BadRequest("DONEBOT_REASONING_UNSUPPORTED", "reasoning field or level is not supported for this model and interface")
)

type DoneBotCatalogResult struct {
	GatewayContractVersion int                   `json:"gateway_contract_version"`
	CatalogScopeID         string                `json:"catalog_scope_id"`
	CatalogVersion         string                `json:"catalog_version"`
	Models                 []DoneBotCatalogModel `json:"models"`
	Defaults               map[string]string     `json:"defaults"`
	ETag                   string                `json:"-"`
}

type DoneBotCatalogModel struct {
	ID                    string                         `json:"id"`
	DisplayName           string                         `json:"display_name"`
	MediaTypes            []string                       `json:"media_types"`
	Platforms             []string                       `json:"platforms"`
	Interfaces            []string                       `json:"interfaces"`
	Capabilities          DoneBotCatalogCapabilities     `json:"capabilities"`
	Pricing               DoneBotCatalogPricing          `json:"pricing"`
	MediaParameters       *DoneBotCatalogMediaParameters `json:"media_parameters,omitempty"`
	MediaFallbackPriority int                            `json:"-"`
}

type DoneBotCatalogCapabilities struct {
	ContextWindowTokens int64                              `json:"context_window_tokens,omitempty"`
	MaxOutputTokens     int64                              `json:"max_output_tokens,omitempty"`
	ToolCalling         bool                               `json:"tool_calling"`
	InputModalities     DoneBotInputModalities             `json:"input_modalities,omitempty"`
	Reasoning           DoneBotCatalogReasoning            `json:"reasoning"`
	ByInterface         map[string]DoneBotCatalogReasoning `json:"by_interface,omitempty"`
}

// DoneBotInputModalities describes declared request inputs, independently of output media types.
type DoneBotInputModalities []string

func (modalities *DoneBotInputModalities) UnmarshalJSON(raw []byte) error {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	if len(values) == 0 || len(values) > 4 {
		return fmt.Errorf("input modalities must declare one to four values")
	}
	for i, value := range values {
		if !slices.Contains([]string{"text", "image", "audio", "video"}, value) || slices.Contains(values[:i], value) {
			return fmt.Errorf("input modalities must be known and unique")
		}
	}
	*modalities = values
	return nil
}

type doneBotCatalogConfiguredCapabilities struct {
	DoneBotCatalogCapabilities
	MediaParameters       *DoneBotCatalogMediaParameters `json:"media_parameters,omitempty"`
	MediaFallbackPriority int                            `json:"media_fallback_priority,omitempty"`
}

type DoneBotCatalogReasoning struct {
	Supported   bool           `json:"supported"`
	Control     string         `json:"control"`
	Levels      []string       `json:"levels"`
	Default     *string        `json:"default"`
	NativeField *string        `json:"native_field"`
	Mapping     map[string]any `json:"mapping,omitempty"`
}

type DoneBotCatalogPricing struct {
	Currency         string             `json:"currency"`
	InputPerMillion  *float64           `json:"input_per_million,omitempty"`
	OutputPerMillion *float64           `json:"output_per_million,omitempty"`
	ImageSizes       map[string]float64 `json:"image_sizes,omitempty"`
	PerRequest       *float64           `json:"per_request,omitempty"`
	Tiers            map[string]float64 `json:"tiers,omitempty"`
	Unit             string             `json:"unit,omitempty"`
}

type DoneBotCatalogService struct {
	installs   *DoneBotService
	admin      *DoneBotAdminService
	imageTasks *ImageTaskService
}

func NewDoneBotCatalogService(installs *DoneBotService, admin *DoneBotAdminService) *DoneBotCatalogService {
	return &DoneBotCatalogService{installs: installs, admin: admin}
}

func ProvideDoneBotCatalogService(installs *DoneBotService, admin *DoneBotAdminService, tasks *ImageTaskService) *DoneBotCatalogService {
	s := NewDoneBotCatalogService(installs, admin)
	s.imageTasks = tasks
	return s
}

func (s *DoneBotCatalogService) Models(ctx context.Context, userID int64, installationUUID string) (*DoneBotCatalogResult, error) {
	install, err := s.installs.InstallationForUser(ctx, userID, installationUUID)
	if err != nil {
		return nil, err
	}
	if install.Status != StatusActive || install.ExpiresAt != nil && !install.ExpiresAt.After(time.Now()) {
		return nil, ErrDoneBotInstallInactive
	}
	key, err := s.installs.keys.GetByID(context.WithValue(ctx, doneBotKeyManagementContext{}, true), install.APIKeyID)
	if err != nil && !errors.Is(err, ErrAPIKeyNotFound) {
		return nil, err
	}
	if err != nil || key == nil || key.UserID != userID || key.GroupID == nil || *key.GroupID != install.GroupID || !key.IsActive() || key.IsExpired() {
		return nil, ErrDoneBotInstallInactive
	}
	inputs, err := s.loadCatalog(ctx, install.GroupID)
	if err != nil {
		return nil, err
	}
	if s.imageTasks != nil && !s.imageTasks.Enabled() {
		inputs.rows, err = doneBotRowsWithoutUnavailableImageTasks(inputs)
		if err != nil {
			return nil, err
		}
	}
	return buildDoneBotPublicCatalog(inputs.group, userID, install.InstallationID, inputs.rows, inputs.accounts, inputs.routes, inputs.capabilities)
}

func doneBotRowsWithoutUnavailableImageTasks(inputs *doneBotCatalogInputs) ([]DoneBotModel, error) {
	rows := make([]DoneBotModel, 0, len(inputs.rows))
	for _, row := range inputs.rows {
		if row.Kind == "images" {
			configured := inputs.capabilities[row.Model].MediaParameters
			media, err := doneBotPublicMediaParameters(inputs.group.ID, row, inputs.accounts, inputs.routes, configured)
			if err != nil {
				return nil, err
			}
			if media != nil && media.Protocol.Adapter == "donebot.images.async.v1" {
				continue
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

type doneBotCatalogInputs struct {
	group        *Group
	rows         []DoneBotModel
	accounts     []Account
	routes       []CompositeModelRoute
	capabilities map[string]doneBotCatalogConfiguredCapabilities
}

func (s *DoneBotCatalogService) loadCatalog(ctx context.Context, groupID int64) (*doneBotCatalogInputs, error) {
	if err := s.installs.enabled(); err != nil {
		return nil, err
	}
	currentGroupID, err := s.installs.groupID(ctx)
	if err != nil {
		return nil, err
	}
	if groupID != currentGroupID {
		return nil, ErrDoneBotGroupUnavailable
	}
	if s.installs.cfg.DoneBot.CatalogCurrency != "CNY" {
		return nil, ErrDoneBotCatalogCurrency
	}
	group, err := s.admin.admin.GetGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group == nil || group.ID != groupID || group.Platform != PlatformComposite || group.Status != StatusActive || group.IsSubscriptionType() {
		return nil, ErrDoneBotGroupUnavailable
	}
	rows, err := s.admin.catalog(ctx, group)
	if err != nil {
		return nil, err
	}
	accounts, err := s.admin.accounts.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	routes, err := s.admin.admin.ListCompositeRoutes(ctx, groupID)
	if err != nil {
		return nil, err
	}
	raw, err := s.admin.settings.GetValue(ctx, doneBotCapabilitiesSetting)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	capabilities, err := parseDoneBotCatalogCapabilities(raw)
	if err != nil {
		return nil, ErrDoneBotCatalogCapabilities
	}
	return &doneBotCatalogInputs{group: group, rows: rows, accounts: accounts, routes: routes, capabilities: capabilities}, nil
}

// ValidateReasoning validates explicit native reasoning controls for an already
// authenticated DoneBot installation key. Callers provide its bound group and
// the actual inbound interface; Gemini model IDs come from the request URL.
func (s *DoneBotCatalogService) ValidateReasoning(ctx context.Context, groupID int64, modelID, iface string, body []byte) error {
	if err := validateDoneBotNativeJSON(body, true); err != nil {
		return ErrDoneBotReasoningUnsupported
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(body, &document); err != nil || document == nil {
		return ErrDoneBotReasoningUnsupported
	}
	paths := []string{"reasoning.effort", "reasoning_effort", "output_config.effort", "thinking.budget_tokens", "generationConfig.thinkingConfig.thinkingBudget", "thinkingConfig.thinkingBudget", "effort", "reasoning.budget_tokens", "thinking.budgetTokens", "thinkingConfig.thinkingLevel", "generationConfig.thinkingConfig.thinkingLevel", "generation_config.thinking_config.thinking_budget", "generation_config.thinking_config.thinking_level"}
	var selectedPath string
	var selectedValue gjson.Result
	for _, path := range paths {
		value, exists := doneBotNativeValue(document, path)
		if !exists {
			continue
		}
		if selectedPath != "" {
			return ErrDoneBotReasoningUnsupported
		}
		selectedPath, selectedValue = path, value
	}
	if selectedPath == "" {
		return nil
	}
	if modelID == "" {
		value, _ := doneBotNativeValue(document, "model")
		modelID = value.String()
	}
	inputs, err := s.loadCatalog(ctx, groupID)
	if err != nil {
		return err
	}
	catalog, err := buildDoneBotPublicCatalog(inputs.group, 0, "", inputs.rows, inputs.accounts, inputs.routes, inputs.capabilities)
	if err != nil {
		return err
	}
	for _, model := range catalog.Models {
		if model.ID != modelID || !slices.Contains(model.Interfaces, iface) {
			continue
		}
		reasoning := model.Capabilities.Reasoning
		if perInterface, ok := model.Capabilities.ByInterface[iface]; ok {
			reasoning = perInterface
		}
		if !reasoning.Supported || reasoning.NativeField == nil {
			return ErrDoneBotReasoningUnsupported
		}
		nativePath := *reasoning.NativeField
		if iface == "gemini.generate_content" && nativePath == "thinkingConfig.thinkingBudget" {
			nativePath = "generationConfig." + nativePath
		}
		if selectedPath != nativePath {
			return ErrDoneBotReasoningUnsupported
		}
		switch reasoning.Control {
		case "effort":
			if selectedValue.Type == gjson.String && slices.Contains(reasoning.Levels, selectedValue.String()) {
				return nil
			}
		case "budget":
			if selectedValue.Type != gjson.Number {
				return ErrDoneBotReasoningUnsupported
			}
			budget := selectedValue.Float()
			if budget <= 0 || math.Trunc(budget) != budget {
				return ErrDoneBotReasoningUnsupported
			}
			for _, level := range reasoning.Levels {
				if reasoning.Mapping[level] == budget {
					return nil
				}
			}
		}
		return ErrDoneBotReasoningUnsupported
	}
	return ErrDoneBotReasoningUnsupported
}

func doneBotNativeValue(document map[string]json.RawMessage, path string) (gjson.Result, bool) {
	segments := strings.Split(path, ".")
	for i, segment := range segments {
		value, exists := document[segment]
		if !exists {
			return gjson.Result{}, false
		}
		if i == len(segments)-1 {
			return gjson.ParseBytes(value), true
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(value, &nested); err != nil || nested == nil {
			return gjson.Result{}, false
		}
		document = nested
	}
	return gjson.Result{}, false
}

func validateDoneBotNativeJSON(body []byte, native bool) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var walk func([]string) error
	walk = func(path []string) error {
		if len(path) > 128 {
			return fmt.Errorf("JSON nesting is too deep")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, composite := token.(json.Delim)
		if !composite {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return fmt.Errorf("duplicate JSON key")
				}
				seen[key] = true
				if native && !doneBotNativeKeyCaseAllowed(path, key) {
					return fmt.Errorf("noncanonical reasoning key")
				}
				if err := walk(append(slices.Clone(path), key)); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walk(append(slices.Clone(path), "[]")); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := walk(nil); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("JSON must contain one value")
	}
	return nil
}

func doneBotNativeKeyCaseAllowed(path []string, key string) bool {
	var sensitive []string
	switch strings.Join(path, ".") {
	case "":
		sensitive = []string{"model", "reasoning", "reasoning_effort", "output_config", "thinking", "thinkingConfig", "generationConfig", "generation_config", "effort"}
	case "reasoning":
		sensitive = []string{"effort", "budget_tokens"}
	case "output_config":
		sensitive = []string{"effort"}
	case "thinking":
		sensitive = []string{"budget_tokens", "budgetTokens"}
	case "generationConfig":
		sensitive = []string{"thinkingConfig"}
	case "thinkingConfig", "generationConfig.thinkingConfig":
		sensitive = []string{"thinkingBudget", "thinkingLevel"}
	case "generation_config":
		sensitive = []string{"thinking_config"}
	case "generation_config.thinking_config":
		sensitive = []string{"thinking_budget", "thinking_level"}
	}
	for _, canonical := range sensitive {
		if strings.EqualFold(key, canonical) && key != canonical {
			return false
		}
	}
	return true
}

func doneBotUnsupportedReasoning() DoneBotCatalogReasoning {
	return DoneBotCatalogReasoning{Control: "none", Levels: []string{}}
}

func parseDoneBotCatalogCapabilities(raw string) (map[string]doneBotCatalogConfiguredCapabilities, error) {
	capabilities := map[string]doneBotCatalogConfiguredCapabilities{}
	if strings.TrimSpace(raw) == "" {
		return capabilities, nil
	}
	if len(raw) > 1<<20 {
		return nil, fmt.Errorf("capabilities configuration exceeds 1 MiB")
	}
	if err := validateDoneBotNativeJSON([]byte(raw), false); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&capabilities); err != nil {
		return nil, err
	}
	if capabilities == nil {
		return nil, fmt.Errorf("capabilities must be a JSON object")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("capabilities must contain one JSON object")
	}
	if len(capabilities) > 4096 {
		return nil, fmt.Errorf("too many model capabilities")
	}
	for model, capability := range capabilities {
		if strings.TrimSpace(model) != model || model == "" || len(model) > 200 || strings.ContainsAny(model, "*\r\n") {
			return nil, fmt.Errorf("invalid capability model")
		}
		if capability.MediaFallbackPriority < 0 || capability.MediaFallbackPriority > 1000 {
			return nil, fmt.Errorf("media fallback priority must be between 0 and 1000")
		}
		if capability.ContextWindowTokens < 0 || capability.MaxOutputTokens < 0 {
			return nil, fmt.Errorf("token capabilities must not be negative")
		}
		if err := validateDoneBotMediaMetadata(capability.MediaParameters); err != nil {
			return nil, err
		}
		if err := validateDoneBotCatalogReasoning(capability.Reasoning, ""); err != nil {
			return nil, err
		}
		for iface, reasoning := range capability.ByInterface {
			if !doneBotTextInterface(iface) {
				return nil, fmt.Errorf("unknown reasoning interface")
			}
			if err := validateDoneBotCatalogReasoning(reasoning, iface); err != nil {
				return nil, err
			}
		}
	}
	return capabilities, nil
}

func validateDoneBotCatalogReasoning(reasoning DoneBotCatalogReasoning, iface string) error {
	if !reasoning.Supported {
		if reasoning.Control != "" && reasoning.Control != "none" || len(reasoning.Levels) != 0 || reasoning.Default != nil || reasoning.NativeField != nil || len(reasoning.Mapping) != 0 {
			return fmt.Errorf("unsupported reasoning must not declare controls")
		}
		return nil
	}
	if reasoning.Control != "effort" && reasoning.Control != "budget" {
		return fmt.Errorf("unknown reasoning control")
	}
	if reasoning.NativeField == nil || !doneBotReasoningFieldAllowed(iface, reasoning.Control, *reasoning.NativeField) {
		return fmt.Errorf("reasoning field is not native to interface")
	}
	if len(reasoning.Levels) == 0 || len(reasoning.Levels) > 16 {
		return fmt.Errorf("reasoning must declare levels")
	}
	previousRank := 0
	for i, level := range reasoning.Levels {
		rank, known := reasoningEffortRank(level)
		if !known || level != NormalizeMaxReasoningEffort(level) || rank <= previousRank || slices.Contains(reasoning.Levels[:i], level) {
			return fmt.Errorf("reasoning levels must be canonical and ordered")
		}
		previousRank = rank
		if reasoning.Control == "budget" {
			budget, ok := reasoning.Mapping[level].(float64)
			if !ok || math.IsInf(budget, 0) || math.IsNaN(budget) || budget <= 0 || budget > 1e9 || math.Trunc(budget) != budget {
				return fmt.Errorf("budget levels require integer token mappings")
			}
		}
	}
	if reasoning.Default != nil && !slices.Contains(reasoning.Levels, *reasoning.Default) {
		return fmt.Errorf("reasoning default must be a declared level")
	}
	for level, value := range reasoning.Mapping {
		if !slices.Contains(reasoning.Levels, level) {
			return fmt.Errorf("reasoning mapping contains unknown level")
		}
		if reasoning.Control == "effort" {
			mapped, ok := value.(string)
			if !ok || mapped != level {
				return fmt.Errorf("effort mappings are controlled by group policy")
			}
		}
	}
	return nil
}

func doneBotTextInterface(iface string) bool {
	return slices.Contains([]string{"anthropic.messages", "openai.responses", "openai.chat_completions", "gemini.generate_content"}, iface)
}

func doneBotReasoningFieldAllowed(iface, control, field string) bool {
	allowed := map[string]map[string][]string{
		"anthropic.messages":      {"effort": {"output_config.effort"}, "budget": {"thinking.budget_tokens"}},
		"openai.responses":        {"effort": {"reasoning.effort"}},
		"openai.chat_completions": {"effort": {"reasoning_effort"}},
		"gemini.generate_content": {"budget": {"thinkingConfig.thinkingBudget", "generationConfig.thinkingConfig.thinkingBudget"}},
	}
	if iface == "" {
		for candidate := range allowed {
			if slices.Contains(allowed[candidate][control], field) {
				return true
			}
		}
		return false
	}
	return slices.Contains(allowed[iface][control], field)
}

func buildDoneBotPublicCatalog(group *Group, userID int64, installation string, rows []DoneBotModel, accounts []Account, routes []CompositeModelRoute, configured map[string]doneBotCatalogConfiguredCapabilities) (*DoneBotCatalogResult, error) {
	result := &DoneBotCatalogResult{GatewayContractVersion: 4, Models: []DoneBotCatalogModel{}, Defaults: map[string]string{}}
	fallbackPriorities := map[string]int{}
	scope := sha256.Sum256([]byte(fmt.Sprintf("donebot:4:%d:%s:%d", userID, installation, group.ID)))
	result.CatalogScopeID = "dbs_" + hex.EncodeToString(scope[:16])
	for _, row := range rows {
		if !row.Enabled || !row.Available || !row.PricingConfigured {
			continue
		}
		interfaces := doneBotPublicInterfaces(group.ID, row, accounts, routes)
		if len(interfaces) == 0 {
			continue
		}
		capability := doneBotPublicCapabilities(group, row.Model, interfaces, configured[row.Model].DoneBotCatalogCapabilities)
		mediaType := map[string]string{"llm": "text", "images": "image", "video": "video", "audio": "audio"}[row.Kind]
		if mediaType == "" {
			continue
		}
		if slices.Contains(interfaces, "openai.music") {
			mediaType = "music"
		}
		pricing := DoneBotCatalogPricing{Currency: "CNY"}
		switch row.Kind {
		case "llm":
			pricing.InputPerMillion = doneBotCatalogPrice(row.EffectiveInputPrice, 1e6)
			pricing.OutputPerMillion = doneBotCatalogPrice(row.EffectiveOutputPrice, 1e6)
		case "images":
			pricing.ImageSizes = doneBotCatalogPrices(row.EffectiveImagePrices)
		case "audio":
			pricing.PerRequest = doneBotCatalogPrice(row.EffectivePerRequestPrice, 1)
			pricing.Tiers = doneBotCatalogPrices(row.EffectiveTierPrices)
			pricing.Unit = row.AudioPriceUnit
		case "video":
			pricing.Tiers = map[string]float64{}
			pricing.Unit = "second"
			if row.EditablePricing != nil {
				for _, tier := range row.EditablePricing.VideoPrices {
					if price := doneBotCatalogPrice(tier.PricePerSecond, 1); price != nil {
						pricing.Tiers[SeedancePricingTier(tier.Resolution, tier.ReferenceVideo)] = *price
					}
				}
			}
		}
		model := DoneBotCatalogModel{ID: row.Model, DisplayName: row.Model, MediaTypes: []string{mediaType}, Platforms: []string{row.Platform}, Interfaces: interfaces, Capabilities: capability, Pricing: pricing, MediaFallbackPriority: configured[row.Model].MediaFallbackPriority}
		media, err := doneBotPublicMediaParameters(group.ID, row, accounts, routes, configured[row.Model].MediaParameters)
		if err != nil {
			return nil, ErrDoneBotCatalogCapabilities
		}
		if row.Kind != "llm" && media == nil {
			continue
		}
		model.MediaParameters = media
		if model.MediaFallbackPriority > 0 && media != nil {
			fallbackPriorities[row.Model] = model.MediaFallbackPriority
		}
		result.Models = append(result.Models, model)
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].ID < result.Models[j].ID })
	for _, model := range result.Models {
		for _, iface := range model.Interfaces {
			if result.Defaults[iface] == "" {
				result.Defaults[iface] = model.ID
			}
		}
	}
	canonical, err := json.Marshal(struct {
		Catalog            *DoneBotCatalogResult `json:"catalog"`
		FallbackPriorities map[string]int        `json:"media_fallback_priorities"`
	}{Catalog: result, FallbackPriorities: fallbackPriorities})
	if err != nil {
		return nil, err
	}
	version := sha256.Sum256(canonical)
	result.CatalogVersion = "dbv_" + hex.EncodeToString(version[:])
	result.ETag = `"` + result.CatalogVersion + `"`
	return result, nil
}

func doneBotCatalogPrice(value *float64, multiplier float64) *float64 {
	if value == nil || math.IsInf(*value, 0) || math.IsNaN(*value) || *value < 0 {
		return nil
	}
	price := *value * multiplier
	if math.IsInf(price, 0) || math.IsNaN(price) {
		return nil
	}
	return &price
}

func doneBotCatalogPrices(values map[string]*float64) map[string]float64 {
	result := map[string]float64{}
	for tier, value := range values {
		if price := doneBotCatalogPrice(value, 1); price != nil {
			result[tier] = *price
		}
	}
	return result
}

func doneBotPublicInterfaces(groupID int64, row DoneBotModel, accounts []Account, routes []CompositeModelRoute) []string {
	interfaces := []string{}
	activeAccounts := []Account{}
	activeRoutes := []CompositeModelRoute{}
	for i := range accounts {
		account := &accounts[i]
		if account.Status == StatusActive && account.Schedulable && slices.Contains(account.GroupIDs, groupID) {
			activeAccounts = append(activeAccounts, *account)
		}
	}
	for _, route := range routes {
		if route.Enabled && (route.GroupID == 0 || route.GroupID == groupID) {
			activeRoutes = append(activeRoutes, route)
		}
	}
	endpoints := map[string]string{"anthropic.messages": CompositeRouteEndpointMessages, "openai.responses": CompositeRouteEndpointResponses, "openai.chat_completions": CompositeRouteEndpointChatCompletions, "gemini.generate_content": CompositeRouteEndpointGemini}
	if row.Kind == "llm" && row.Platform == PlatformTypeSafe {
		endpoints = map[string]string{"typesafe.systemone": CompositeRouteEndpointAny}
	}
	if row.Kind == "images" {
		endpoints = map[string]string{"openai.images": CompositeRouteEndpointImages}
	}
	if row.Kind == "video" {
		endpoints = map[string]string{"openai.videos": CompositeRouteEndpointAny}
	}
	if row.Kind == "audio" {
		endpoints = map[string]string{}
		if row.AudioPriceUnit == "million_characters" {
			endpoints["openai.audio.speech"] = "audio_speech"
		}
		if row.AudioPriceUnit == "request" {
			endpoints["openai.music"] = "music"
		}
	}
	for iface, endpoint := range endpoints {
		upstream := row.Model
		if row.routingModel != "" {
			if row.routingEndpoint != CompositeRouteEndpointAny && row.routingEndpoint != endpoint {
				continue
			}
			upstream = row.routingModel
		} else if route, matched := matchCompositeRoute(activeRoutes, row.Model, endpoint); matched {
			if route.TargetPlatform != row.Platform {
				continue
			}
			if route.UpstreamModel != "" {
				upstream = route.UpstreamModel
			}
		} else {
			owners := map[string]bool{}
			for i := range activeAccounts {
				if doneBotCatalogRawMappingSupports(&activeAccounts[i], row.Model) {
					owners[activeAccounts[i].Platform] = true
				}
			}
			if len(owners) != 1 || !owners[row.Platform] {
				continue
			}
		}
		candidates := 0
		allNative := true
		for i := range activeAccounts {
			account := &activeAccounts[i]
			if account.Platform != row.Platform || !doneBotCatalogRawMappingSupports(account, upstream) {
				continue
			}
			capabilityEndpoint := endpoint
			if row.Kind == "llm" && account.IsOpenAICompatible() {
				capabilityEndpoint = CompositeRouteEndpointChatCompletions
			}
			if !doneBotCatalogAccountSupports(account, upstream, capabilityEndpoint) {
				continue
			}
			candidates++
			if !doneBotAccountUsesNativeInterface(account, upstream, row.Kind, iface) {
				allNative = false
				break
			}
		}
		if candidates > 0 && allNative {
			interfaces = append(interfaces, iface)
		}
	}
	sort.Strings(interfaces)
	return interfaces
}

func doneBotAccountUsesNativeInterface(account *Account, model, kind, iface string) bool {
	if kind == "images" {
		return iface == "openai.images" && (account.Platform == PlatformOpenAI || account.Platform == PlatformGrok)
	}
	if kind == "audio" {
		return doneBotHasAudioAccount(model, account.Platform, []Account{*account})
	}
	if kind == "video" {
		return iface == "openai.videos" && doneBotHasNativeMediaAccount(model, account.Platform, "video", []Account{*account})
	}
	switch iface {
	case "typesafe.systemone":
		return account.Platform == PlatformTypeSafe
	case "anthropic.messages":
		return account.Platform == PlatformAnthropic
	case "gemini.generate_content":
		return account.Platform == PlatformGemini
	case "openai.responses", "openai.chat_completions":
		if !account.IsOpenAICompatible() {
			return false
		}
		inbound := APIProtocolChatCompletions
		if iface == "openai.responses" {
			inbound = APIProtocolResponses
		}
		if account.routesByModel() {
			if account.GetAPIProtocol() == APIProtocolAdaptive {
				rules, configured := account.configuredProtocolRules()
				if !configured {
					return false
				}
				if _, matched := matchProtocolRuleSet(account.GetMappedModel(model), rules); !matched {
					return false
				}
			}
		} else if account.Platform == PlatformGrok {
			return iface == "openai.chat_completions" && account.Type == AccountTypeAPIKey
		} else if !account.RoutesProtocolByInbound() && iface == "openai.responses" {
			if !account.IsOpenAIOAuth() && openai_compat.ResolveResponsesSupport(account.Extra) != openai_compat.ResponsesSupportYes {
				return false
			}
		}
		return resolveUpstreamProtocol(account, inbound, account.GetMappedModel(model), nil) == inbound
	default:
		return false
	}
}

func doneBotPublicCapabilities(group *Group, model string, interfaces []string, configured DoneBotCatalogCapabilities) DoneBotCatalogCapabilities {
	if !slices.ContainsFunc(interfaces, doneBotTextInterface) {
		return DoneBotCatalogCapabilities{InputModalities: slices.Clone(configured.InputModalities), Reasoning: doneBotUnsupportedReasoning()}
	}
	capability := DoneBotCatalogCapabilities{ContextWindowTokens: configured.ContextWindowTokens, MaxOutputTokens: configured.MaxOutputTokens, ToolCalling: true, InputModalities: slices.Clone(configured.InputModalities), Reasoning: doneBotUnsupportedReasoning()}
	byInterface := map[string]DoneBotCatalogReasoning{}
	for _, iface := range interfaces {
		if !doneBotTextInterface(iface) {
			continue
		}
		reasoning, explicit := configured.ByInterface[iface]
		if !explicit {
			reasoning = configured.Reasoning
		}
		if !reasoning.Supported || reasoning.NativeField == nil || !doneBotReasoningFieldAllowed(iface, reasoning.Control, *reasoning.NativeField) {
			continue
		}
		filtered := doneBotPublicReasoning(group, model, reasoning)
		if filtered.Supported {
			byInterface[iface] = filtered
		}
	}
	if len(byInterface) == 0 {
		return capability
	}
	capability.ByInterface = byInterface
	var common *DoneBotCatalogReasoning
	for _, iface := range interfaces {
		reasoning, exists := byInterface[iface]
		if !exists {
			return capability
		}
		if common == nil {
			copy := reasoning
			common = &copy
			continue
		}
		left, _ := json.Marshal(common)
		right, _ := json.Marshal(reasoning)
		if !bytes.Equal(left, right) {
			return capability
		}
	}
	if common != nil {
		capability.Reasoning = *common
	}
	return capability
}

func doneBotPublicReasoning(group *Group, model string, source DoneBotCatalogReasoning) DoneBotCatalogReasoning {
	result := DoneBotCatalogReasoning{Supported: true, Control: source.Control, Levels: []string{}, NativeField: source.NativeField, Mapping: map[string]any{}}
	if source.Control != "effort" {
		if group.MaxReasoningEffort != "" || len(group.ReasoningEffortMappings) != 0 {
			return doneBotUnsupportedReasoning()
		}
		result.Levels = append(result.Levels, source.Levels...)
		for level, value := range source.Mapping {
			result.Mapping[level] = value
		}
		result.Default = source.Default
		return result
	}
	maxRank, hasMax := reasoningEffortRank(group.MaxReasoningEffort)
	for _, requested := range source.Levels {
		effective, _ := mapReasoningEffort(requested, group.ReasoningEffortMappings, model)
		if isReasoningEffortMappingDeny(effective) {
			continue
		}
		rank, known := reasoningEffortRank(effective)
		if !known {
			continue
		}
		if hasMax && rank > maxRank {
			if NormalizeMaxReasoningEffortOverLimit(group.MaxReasoningEffortOverLimit) == ReasoningEffortOverLimitDeny {
				continue
			}
			effective = NormalizeMaxReasoningEffort(group.MaxReasoningEffort)
		}
		if !slices.Contains(source.Levels, effective) {
			continue
		}
		result.Levels = append(result.Levels, requested)
		if requested != effective {
			result.Mapping[requested] = effective
		}
	}
	if len(result.Levels) == 0 {
		return doneBotUnsupportedReasoning()
	}
	if source.Default != nil && slices.Contains(result.Levels, *source.Default) {
		result.Default = source.Default
	}
	return result
}
