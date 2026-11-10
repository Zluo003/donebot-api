package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const doneBotMediaIntentFallbackLimit = 2

type DoneBotMediaIntentPlan struct {
	Kind               string
	Operation          string
	ModelID            string
	NativeKind         string
	Path               string
	Body               []byte
	Candidates         []string
	Plans              []DoneBotMediaIntentCandidatePlan
	DispatchCandidates []DoneBotDispatchCandidate
	EstimatedCents     *json.Number
}

type DoneBotMediaIntentCandidatePlan struct {
	PublicModel      string
	RoutingModel     string
	BillingModel     string
	ForwardModel     string
	Protocol         string
	Kind             string
	NativeKind       string
	Path             string
	Body             []byte
	ContentType      string
	NativeParameters map[string]any
	Info             APIMartRequestInfo
	VideoInfo        GrokMediaRequestInfo
	EstimatedCents   *json.Number
}

type DoneBotMediaIntentAssetReader interface {
	Get(context.Context, int64, string, string) (*GatewayAsset, error)
}

func DoneBotMediaIntentModerationBody(ctx context.Context, request DoneBotMediaIntentRequest, assets DoneBotMediaIntentAssetReader, ownerKeyID int64) ([]byte, error) {
	text := []string{request.Parameters.Prompt}
	optionNames := make([]string, 0, len(request.Parameters.Options))
	for name := range request.Parameters.Options {
		optionNames = append(optionNames, name)
	}
	sort.Strings(optionNames)
	for _, name := range optionNames {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(request.Parameters.Options[name]))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, ErrDoneBotMediaInvalid
		}
		appendDoneBotMediaIntentModerationText(value, &text)
	}
	images := make([]string, 0, len(request.Parameters.References))
	seen := make(map[string]struct{}, len(request.Parameters.References))
	for _, reference := range request.Parameters.References {
		if reference.Type != "image" {
			continue
		}
		if _, ok := seen[reference.AssetID]; ok {
			continue
		}
		if assets == nil || ownerKeyID <= 0 {
			return nil, ErrAssetNotFound
		}
		asset, err := assets.Get(ctx, ownerKeyID, reference.AssetID, "")
		if err != nil || asset == nil || asset.ID != reference.AssetID {
			return nil, ErrAssetNotFound
		}
		mediaType, _, err := mime.ParseMediaType(asset.MIME)
		parsedURL, urlErr := url.Parse(asset.URL)
		if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "image/") || urlErr != nil ||
			(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.Fragment != "" {
			return nil, ErrAssetNotFound
		}
		seen[reference.AssetID] = struct{}{}
		images = append(images, asset.URL)
	}
	return GrokMediaRequestInfo{Prompt: strings.Join(text, "\n"), InputImageURLs: images}.ModerationBody(), nil
}

func appendDoneBotMediaIntentModerationText(value any, text *[]string) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			*text = append(*text, typed)
		}
	case []any:
		for _, child := range typed {
			appendDoneBotMediaIntentModerationText(child, text)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			appendDoneBotMediaIntentModerationText(typed[key], text)
		}
	}
}

func (s *DoneBotCatalogService) PlanMediaIntent(ctx context.Context, owner DoneBotMediaOwner, request DoneBotMediaIntentRequest, assets DoneBotMediaIntentAssetReader) (*DoneBotMediaIntentPlan, error) {
	if err := s.validateMediaIntentOwner(ctx, owner); err != nil {
		return nil, err
	}
	catalog, err := s.Models(ctx, owner.UserID, owner.InstallationID)
	if err != nil {
		return nil, err
	}
	inputs, err := s.loadCatalog(ctx, owner.GroupID)
	if err != nil {
		return nil, err
	}
	nativeKind, defaultInterface, err := doneBotMediaIntentTarget(request)
	if err != nil {
		return nil, err
	}
	candidateIDs := doneBotMediaIntentCandidateIDs(catalog, defaultInterface)
	plans := make([]DoneBotMediaIntentCandidatePlan, 0, len(candidateIDs))
	dispatchCandidates := make([]DoneBotDispatchCandidate, 0, len(candidateIDs))
	for _, modelID := range candidateIDs {
		var row *DoneBotModel
		for i := range inputs.rows {
			if inputs.rows[i].Model == modelID {
				row = &inputs.rows[i]
				break
			}
		}
		if row == nil {
			continue
		}
		plan, planErr := planDoneBotMediaIntentForCatalogModel(ctx, s, owner, request, assets, catalog, modelID, nativeKind)
		if planErr != nil {
			if errors.Is(planErr, ErrDoneBotMediaInvalid) {
				continue
			}
			return nil, planErr
		}
		candidate, candidateErr := buildDoneBotMediaIntentCandidate(*row, request, *plan)
		if candidateErr != nil {
			if errors.Is(candidateErr, ErrDoneBotMediaInvalid) {
				continue
			}
			return nil, candidateErr
		}
		plans = append(plans, *candidate)
		dispatchCandidates = append(dispatchCandidates, DoneBotDispatchCandidate{
			PublicModel: candidate.PublicModel, RoutingModel: candidate.RoutingModel, BillingModel: candidate.BillingModel,
			ForwardModel: candidate.ForwardModel, Kind: candidate.Kind, Protocol: candidate.Protocol, Path: candidate.Path,
			ContentType: candidate.ContentType, Body: candidate.Body, Info: candidate.Info, VideoInfo: candidate.VideoInfo,
			References: append([]DoneBotMediaIntentReference(nil), request.Parameters.References...),
		})
	}
	if len(plans) == 0 {
		return nil, ErrDoneBotMediaIntentUnavailable
	}
	selected := plans[0]
	modelIDs := make([]string, 0, len(plans))
	for _, plan := range plans {
		modelIDs = append(modelIDs, plan.PublicModel)
	}
	hold, err := DoneBotMediaIntentMaximumEstimate(plans)
	if err != nil {
		return nil, err
	}
	return &DoneBotMediaIntentPlan{
		Kind: request.Intent.Kind, Operation: request.Intent.Operation,
		ModelID: selected.PublicModel, NativeKind: selected.NativeKind, Path: selected.Path,
		Body: selected.Body, Candidates: modelIDs, Plans: plans, DispatchCandidates: dispatchCandidates, EstimatedCents: hold,
	}, nil
}

func buildDoneBotMediaIntentCandidate(row DoneBotModel, request DoneBotMediaIntentRequest, plan DoneBotMediaIntentCandidatePlan) (*DoneBotMediaIntentCandidatePlan, error) {
	model := plan.PublicModel
	routingModel := firstNonEmpty(row.routingModel, row.Model)
	billingModel := firstNonEmpty(row.pricingModel, row.Model)
	protocol := ""
	switch planPath := plan.Path; planPath {
	case "/v1/images/generations/async", "/v1/images/edits/async":
		protocol = DoneBotDispatchImages
	case "/v1/images/generations":
		protocol = DoneBotDispatchAPIMartImages
	case "/v1/videos":
		protocol = DoneBotDispatchSeedance
	case "/v1/music/generations":
		protocol = DoneBotDispatchAPIMartMusic
	default:
		return nil, ErrDoneBotMediaInvalid
	}
	out := plan
	out.RoutingModel, out.BillingModel, out.ForwardModel = routingModel, billingModel, routingModel
	out.Protocol, out.Kind, out.ContentType = protocol, request.Intent.Kind, "application/json"
	if protocol == DoneBotDispatchAPIMartImages {
		info, err := ParseAPIMartRequest(APIMartImages, plan.Body)
		if err != nil {
			return nil, ErrDoneBotMediaInvalid
		}
		info.BillingModel = billingModel
		out.Info = info
	}
	if protocol == DoneBotDispatchAPIMartMusic {
		info, err := ParseAPIMartRequest(APIMartSuno, plan.Body)
		if err != nil {
			return nil, ErrDoneBotMediaInvalid
		}
		info.BillingModel = billingModel
		out.Info = info
	}
	if protocol == DoneBotDispatchSeedance {
		resolution, _ := plan.NativeParameters["resolution"].(string)
		duration, ok := doneBotMediaInteger(plan.NativeParameters["duration"])
		if !ok || duration <= 0 {
			return nil, ErrDoneBotMediaInvalid
		}
		hasReferenceVideo := false
		for _, reference := range request.Parameters.References {
			if reference.Type == "video" {
				hasReferenceVideo = true
			}
		}
		out.VideoInfo = GrokMediaRequestInfo{Model: model, Prompt: request.Parameters.Prompt, N: 1, Resolution: resolution,
			DurationSeconds: duration, HasReferenceVideo: hasReferenceVideo}
	}
	return &out, nil
}

// PlanMediaIntentForModel only materializes a model that was admitted as the
// catalog default or one of the two explicitly ordered fallback candidates.
func (s *DoneBotCatalogService) PlanMediaIntentForModel(ctx context.Context, owner DoneBotMediaOwner, request DoneBotMediaIntentRequest, assets DoneBotMediaIntentAssetReader, modelID string) (*DoneBotMediaIntentCandidatePlan, error) {
	if err := s.validateMediaIntentOwner(ctx, owner); err != nil {
		return nil, err
	}
	catalog, err := s.Models(ctx, owner.UserID, owner.InstallationID)
	if err != nil {
		return nil, err
	}
	nativeKind, defaultInterface, err := doneBotMediaIntentTarget(request)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(doneBotMediaIntentCandidateIDs(catalog, defaultInterface), modelID) {
		return nil, ErrDoneBotMediaInvalid
	}
	return planDoneBotMediaIntentForCatalogModel(ctx, s, owner, request, assets, catalog, modelID, nativeKind)
}

func (s *DoneBotCatalogService) validateMediaIntentOwner(ctx context.Context, owner DoneBotMediaOwner) error {
	install, err := s.installs.InstallationForUser(ctx, owner.UserID, owner.InstallationID)
	if err != nil || install == nil || install.APIKeyID != owner.APIKeyID || install.GroupID != owner.GroupID {
		return ErrDoneBotMediaInvalid
	}
	return nil
}

func doneBotMediaIntentTarget(request DoneBotMediaIntentRequest) (nativeKind, defaultInterface string, err error) {
	if !doneBotMediaIntentOperation(request.Intent.Kind, request.Intent.Operation) {
		return "", "", ErrDoneBotMediaInvalid
	}
	switch request.Intent.Kind {
	case "image":
		return "image", "openai.images", nil
	case "video":
		return "video", "openai.videos", nil
	case "audio":
		return "music", "openai.music", nil
	default:
		return "", "", ErrDoneBotMediaInvalid
	}
}

func doneBotMediaIntentCandidateIDs(catalog *DoneBotCatalogResult, defaultInterface string) []string {
	if catalog == nil {
		return nil
	}
	models := make(map[string]*DoneBotCatalogModel, len(catalog.Models))
	for i := range catalog.Models {
		models[catalog.Models[i].ID] = &catalog.Models[i]
	}
	defaultID := catalog.Defaults[defaultInterface]
	if model := models[defaultID]; model == nil || !slices.Contains(model.Interfaces, defaultInterface) {
		defaultID = ""
	}
	candidates := make([]*DoneBotCatalogModel, 0, len(catalog.Models))
	for i := range catalog.Models {
		model := &catalog.Models[i]
		if model.MediaFallbackPriority > 0 && model.ID != defaultID && slices.Contains(model.Interfaces, defaultInterface) && model.MediaParameters != nil {
			candidates = append(candidates, model)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].MediaFallbackPriority != candidates[j].MediaFallbackPriority {
			return candidates[i].MediaFallbackPriority < candidates[j].MediaFallbackPriority
		}
		return candidates[i].ID < candidates[j].ID
	})
	ids := make([]string, 0, 1+doneBotMediaIntentFallbackLimit)
	if defaultID != "" {
		ids = append(ids, defaultID)
	}
	fallbacks := 0
	for _, candidate := range candidates {
		if fallbacks >= doneBotMediaIntentFallbackLimit {
			break
		}
		ids = append(ids, candidate.ID)
		fallbacks++
	}
	return ids
}

func planDoneBotMediaIntentForCatalogModel(ctx context.Context, catalogService *DoneBotCatalogService, owner DoneBotMediaOwner, request DoneBotMediaIntentRequest, assets DoneBotMediaIntentAssetReader, catalog *DoneBotCatalogResult, modelID, nativeKind string) (*DoneBotMediaIntentCandidatePlan, error) {
	var model *DoneBotCatalogModel
	for i := range catalog.Models {
		if catalog.Models[i].ID == modelID {
			model = &catalog.Models[i]
			break
		}
	}
	if model == nil || model.MediaParameters == nil || model.MediaParameters.Protocol == nil || !slices.Contains(model.MediaParameters.Operations, request.Intent.Operation) {
		return nil, ErrDoneBotMediaInvalid
	}
	media := model.MediaParameters
	path := doneBotMediaIntentPath(request.Intent.Kind, request.Intent.Operation, media.Protocol.Adapter)
	if path == "" {
		return nil, ErrDoneBotMediaInvalid
	}
	options, explicitlySet, err := decodeDoneBotMediaIntentOptions(request)
	if err != nil {
		return nil, err
	}
	nativeOptions, err := resolveDoneBotMediaIntentOptions(media, options)
	if err != nil {
		return nil, err
	}
	body, parameters, err := buildDoneBotMediaIntentBody(ctx, request, model, nativeKind, nativeOptions, explicitlySet, assets, owner.APIKeyID)
	if err != nil {
		return nil, err
	}
	if err := catalogService.ValidateMediaIntentPath(ctx, owner, nativeKind, model.ID, request.Intent.Operation, path, parameters); err != nil {
		return nil, err
	}
	estimate, err := catalogService.EstimateMedia(ctx, owner, nativeKind, model.ID, parameters)
	if err != nil {
		return nil, err
	}
	return &DoneBotMediaIntentCandidatePlan{PublicModel: model.ID, NativeKind: nativeKind, Path: path, Body: body, NativeParameters: parameters, EstimatedCents: estimate}, nil
}

// DoneBotMediaIntentMaximumEstimate returns a hold amount only when every
// selectable public-model candidate has a known estimate. A missing estimate
// cannot safely bound what the dispatcher may charge after fallback.
func DoneBotMediaIntentMaximumEstimate(plans []DoneBotMediaIntentCandidatePlan) (*json.Number, error) {
	if len(plans) == 0 {
		return nil, ErrDoneBotMediaIntentEstimateUnavailable
	}
	var maximum decimal.Decimal
	for index, plan := range plans {
		if plan.EstimatedCents == nil {
			return nil, ErrDoneBotMediaIntentEstimateUnavailable
		}
		amount, err := decimal.NewFromString(plan.EstimatedCents.String())
		if err != nil || amount.IsNegative() {
			return nil, ErrDoneBotMediaIntentEstimateUnavailable
		}
		if index == 0 || amount.GreaterThan(maximum) {
			maximum = amount
		}
	}
	amount := json.Number(maximum.String())
	return &amount, nil
}

func decodeDoneBotMediaIntentOptions(request DoneBotMediaIntentRequest) (map[string]any, map[string]bool, error) {
	options := make(map[string]any, len(request.Parameters.Options))
	explicitlySet := make(map[string]bool, len(request.Parameters.Options))
	for key, raw := range request.Parameters.Options {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, ErrDoneBotMediaInvalid
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return nil, nil, ErrDoneBotMediaInvalid
		}
		options[key], explicitlySet[key] = value, true
	}
	return options, explicitlySet, nil
}

func resolveDoneBotMediaIntentOptions(media *DoneBotCatalogMediaParameters, supplied map[string]any) (map[string]any, error) {
	if media == nil || media.IntentOptions == nil {
		if len(supplied) != 0 {
			return nil, ErrDoneBotMediaInvalid
		}
		return map[string]any{}, nil
	}
	for key := range supplied {
		if _, ok := media.IntentOptions[key]; !ok {
			return nil, ErrDoneBotMediaInvalid
		}
	}
	resolved := make(map[string]any, len(media.IntentOptions))
	for name, definition := range media.IntentOptions {
		value, exists := supplied[name]
		if !exists {
			value = definition.Default
		}
		if value == nil {
			continue
		}
		switch definition.Type {
		case "enum":
			selected, ok := value.(string)
			if !ok || !slices.Contains(definition.Values, selected) {
				return nil, ErrDoneBotMediaInvalid
			}
			resolved[name] = selected
		case "integer":
			selected, ok := doneBotMediaInteger(value)
			if !ok || definition.Min == nil || definition.Max == nil || definition.Step < 1 || selected != -1 && (selected < *definition.Min || selected > *definition.Max || (selected-*definition.Min)%definition.Step != 0) || selected == -1 && definition.Automatic == nil {
				return nil, ErrDoneBotMediaInvalid
			}
			resolved[name] = selected
		case "boolean":
			selected, ok := value.(bool)
			if !ok {
				return nil, ErrDoneBotMediaInvalid
			}
			resolved[name] = selected
		default:
			return nil, ErrDoneBotMediaInvalid
		}
	}
	return resolved, nil
}

func doneBotMediaIntentPath(kind, operation, adapter string) string {
	switch kind {
	case "image":
		switch adapter {
		case "donebot.images.async.v1":
			if operation == "generate" {
				return "/v1/images/generations/async"
			}
			if operation == "edit" {
				return "/v1/images/edits/async"
			}
		case "apimart.images.v1":
			if operation == "generate" {
				return "/v1/images/generations"
			}
		}
	case "video":
		if adapter == "seedance.videos.v1" && slices.Contains([]string{"generate", "edit", "extend"}, operation) {
			return "/v1/videos"
		}
	case "audio":
		if operation != "generate" {
			return ""
		}
		switch adapter {
		case "apimart.music.v1":
			return "/v1/music/generations"
		}
	}
	return ""
}

func buildDoneBotMediaIntentBody(ctx context.Context, request DoneBotMediaIntentRequest, model *DoneBotCatalogModel, nativeKind string, options map[string]any, explicitlySet map[string]bool, assets DoneBotMediaIntentAssetReader, ownerKeyID int64) ([]byte, map[string]any, error) {
	media := model.MediaParameters
	if media == nil || media.Protocol == nil {
		return nil, nil, ErrDoneBotMediaInvalid
	}
	params := map[string]any{"model": model.ID}
	for name, value := range options {
		definition, ok := media.IntentOptions[name]
		if !ok || definition.NativeField == "" {
			return nil, nil, ErrDoneBotMediaInvalid
		}
		params[definition.NativeField] = value
	}
	assetURLs := make(map[string]string, len(request.Parameters.References))
	for _, reference := range request.Parameters.References {
		if assets == nil {
			return nil, nil, ErrDoneBotMediaInvalid
		}
		if _, exists := assetURLs[reference.AssetID]; exists {
			continue
		}
		asset, err := assets.Get(ctx, ownerKeyID, reference.AssetID, "")
		if err != nil || asset == nil || !strings.HasPrefix(strings.ToLower(asset.MIME), strings.ToLower(reference.Type)+"/") || strings.TrimSpace(asset.URL) == "" {
			return nil, nil, ErrAssetNotFound
		}
		assetURLs[reference.AssetID] = asset.URL
	}
	assetURL := func(reference DoneBotMediaIntentReference, expected string) (string, error) {
		if reference.Type != expected {
			return "", ErrDoneBotMediaInvalid
		}
		url, ok := assetURLs[reference.AssetID]
		if !ok {
			return "", ErrDoneBotMediaInvalid
		}
		return url, nil
	}
	for _, reference := range request.Parameters.References {
		if _, ok := assetURLs[reference.AssetID]; !ok {
			return nil, nil, ErrDoneBotMediaInvalid
		}
	}
	switch request.Intent.Kind {
	case "image":
		if strings.TrimSpace(request.Parameters.Prompt) == "" {
			return nil, nil, ErrDoneBotMediaInvalid
		}
		params["prompt"] = request.Parameters.Prompt
		refs := make([]string, 0, len(request.Parameters.References))
		for _, reference := range request.Parameters.References {
			url, err := assetURL(reference, "image")
			if err != nil {
				return nil, nil, err
			}
			refs = append(refs, url)
		}
		switch media.Protocol.Adapter {
		case "apimart.images.v1":
			if request.Intent.Operation == "edit" || len(refs) > 10 {
				return nil, nil, ErrDoneBotMediaInvalid
			}
			if len(refs) > 0 {
				params["image_urls"] = refs
			}
		case "openai.images.v1", "donebot.images.async.v1":
			if request.Intent.Operation == "edit" && len(refs) == 0 {
				return nil, nil, ErrDoneBotMediaInvalid
			}
			if len(refs) > 0 {
				images := make([]map[string]string, 0, len(refs))
				for _, ref := range refs {
					images = append(images, map[string]string{"image_url": ref})
				}
				params["images"] = images
			}
		}
	case "video":
		if strings.TrimSpace(request.Parameters.Prompt) == "" {
			return nil, nil, ErrDoneBotMediaInvalid
		}
		content := []any{map[string]any{"type": "text", "text": request.Parameters.Prompt}}
		hasReferenceVideo := false
		for _, reference := range request.Parameters.References {
			url, err := assetURL(reference, reference.Type)
			if err != nil {
				return nil, nil, err
			}
			switch reference.Type {
			case "image":
				content = append(content, map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]string{"url": url}})
			case "video":
				hasReferenceVideo = true
				content = append(content, map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]string{"url": url}})
			default:
				return nil, nil, ErrDoneBotMediaInvalid
			}
		}
		params["content"] = content
		if request.Intent.Operation == "edit" || request.Intent.Operation == "extend" {
			if !hasReferenceVideo {
				return nil, nil, ErrDoneBotMediaInvalid
			}
			if explicitlySet["aspect_ratio"] && options["aspect_ratio"] != "adaptive" || explicitlySet["duration"] && request.Intent.Operation == "edit" && options["duration"] != -1 {
				return nil, nil, ErrDoneBotMediaInvalid
			}
			params["ratio"] = "adaptive"
			params["duration"] = -1
			if request.Intent.Operation == "extend" && explicitlySet["duration"] {
				params["duration"] = options["duration"]
			}
			params["omni_reference_task_type"] = request.Intent.Operation
		} else if hasReferenceVideo {
			params["omni_reference_task_type"] = "reference"
		}
	case "audio":
		if strings.TrimSpace(request.Parameters.Prompt) == "" || len(request.Parameters.References) != 0 {
			return nil, nil, ErrDoneBotMediaInvalid
		}
		if nativeKind != "music" || media.Protocol.Adapter != "apimart.music.v1" {
			return nil, nil, ErrDoneBotMediaInvalid
		}
		params["prompt"] = request.Parameters.Prompt
		if explicitlySet["duration"] || params["max_mode"] == true {
			if explicitlySet["custom"] && params["custom"] != true {
				return nil, nil, ErrDoneBotMediaInvalid
			}
			params["custom"] = true
		}
	default:
		return nil, nil, ErrDoneBotMediaInvalid
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, nil, ErrDoneBotMediaInvalid
	}
	decoded, err := DecodeDoneBotMediaParameters(body)
	if err != nil {
		return nil, nil, err
	}
	return body, decoded, nil
}

var ErrDoneBotMediaIntentUnavailable = infraerrors.New(http.StatusUnprocessableEntity, "DONEBOT_MEDIA_NO_COMPATIBLE_MODEL", "no saleable model supports this media intent and its registered parameters")
var ErrDoneBotMediaIntentEstimateUnavailable = infraerrors.New(http.StatusUnprocessableEntity, "DONEBOT_MEDIA_ESTIMATE_UNAVAILABLE", "a safe prepayment amount cannot be determined for every compatible media model")
