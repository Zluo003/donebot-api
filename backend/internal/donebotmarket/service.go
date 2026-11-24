package donebotmarket

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type BalanceCacheInvalidator func(ctx context.Context, userID int64)

type Service struct {
	db                *sql.DB
	enabled           bool
	httpClient        *http.Client
	reviewCaller      ReviewCaller
	invalidateBalance BalanceCacheInvalidator

	reviewKick chan struct{}
	workerOnce sync.Once
	stopCh     chan struct{}
}

func New(db *sql.DB, enabled bool) *Service {
	return &Service{
		db:         db,
		enabled:    enabled,
		httpClient: &http.Client{Timeout: 55 * time.Second},
		reviewKick: make(chan struct{}, 16),
		stopCh:     make(chan struct{}),
	}
}

func (s *Service) SetReviewCaller(fn ReviewCaller) {
	if s != nil {
		s.reviewCaller = fn
	}
}

func (s *Service) SetBalanceInvalidator(fn BalanceCacheInvalidator) {
	if s != nil {
		s.invalidateBalance = fn
	}
}

func (s *Service) Enabled() bool { return s != nil && s.enabled && s.db != nil }

func (s *Service) available() error {
	if !s.Enabled() {
		return infraerrors.ServiceUnavailable("BOT_MARKET_UNAVAILABLE", "Bot 广场服务暂不可用。")
	}
	return nil
}

func (s *Service) kickReviewWorker() {
	if s == nil {
		return
	}
	select {
	case s.reviewKick <- struct{}{}:
	default:
	}
}

// StartBackgroundWorkers launches the async AI Review Agent queue and periodic freeze-thaw loop.
func (s *Service) StartBackgroundWorkers() {
	if !s.Enabled() {
		return
	}
	s.workerOnce.Do(func() {
		go func() {
			reviewTick := time.NewTicker(15 * time.Second)
			thawTick := time.NewTicker(5 * time.Minute)
			defer reviewTick.Stop()
			defer thawTick.Stop()
			// Process any pending or previously stranded releases immediately on boot.
			s.kickReviewWorker()
			for {
				select {
				case <-s.stopCh:
					return
				case <-s.reviewKick:
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					s.ProcessPendingReviews(ctx, 5)
					cancel()
				case <-reviewTick.C:
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					s.ProcessPendingReviews(ctx, 5)
					cancel()
				case <-thawTick.C:
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					_, _ = s.ThawAllMaturedRewards(ctx)
					cancel()
				}
			}
		}()
	})
}

func (s *Service) StopBackgroundWorkers() {
	if s == nil {
		return
	}
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

// --- Settings ---

func (s *Service) GetSettings(ctx context.Context) (*Settings, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	var out Settings
	var reviewAccountID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, auto_publish_on_ai_approve, review_account_id, review_model, review_api_base, review_api_key,
		       attribution_days, freeze_days, min_withdraw_amount, transfer_bonus_percent,
		       rebate_llm_percent, rebate_image_percent, rebate_video_percent, rebate_music_percent,
		       signing_secret, updated_at
		FROM donebot_market_settings WHERE id = 1`).Scan(
		&out.Enabled, &out.AutoPublishOnAIApprove, &reviewAccountID, &out.ReviewModel, &out.ReviewAPIBase, &out.reviewAPIKey,
		&out.AttributionDays, &out.FreezeDays, &out.MinWithdrawAmount, &out.TransferBonusPercent,
		&out.RebateLLMPercent, &out.RebateImagePercent, &out.RebateVideoPercent, &out.RebateMusicPercent,
		&out.signingSecret, &out.UpdatedAt,
	)
	if reviewAccountID.Valid && reviewAccountID.Int64 > 0 {
		id := reviewAccountID.Int64
		out.ReviewAccountID = &id
	}
	if errors.Is(err, sql.ErrNoRows) {
		secret, genErr := generateSigningSecret()
		if genErr != nil {
			return nil, genErr
		}
		_, _ = s.db.ExecContext(ctx, `INSERT INTO donebot_market_settings (id, signing_secret) VALUES (1, $1) ON CONFLICT (id) DO NOTHING`, secret)
		return s.GetSettings(ctx)
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.signingSecret) == "" {
		secret, genErr := generateSigningSecret()
		if genErr != nil {
			return nil, genErr
		}
		var persisted string
		_ = s.db.QueryRowContext(ctx, `
			UPDATE donebot_market_settings
			SET signing_secret = CASE WHEN signing_secret = '' THEN $1 ELSE signing_secret END,
			    updated_at = NOW()
			WHERE id = 1
			RETURNING signing_secret`, secret).Scan(&persisted)
		if persisted != "" {
			out.signingSecret = persisted
		} else {
			out.signingSecret = secret
		}
	}
	out.ReviewAPIKeyConfigured = strings.TrimSpace(out.reviewAPIKey) != ""
	return &out, nil
}

func (s *Service) UpdateSettings(ctx context.Context, in UpdateSettingsInput) (*Settings, error) {
	cur, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	if in.AutoPublishOnAIApprove != nil {
		cur.AutoPublishOnAIApprove = *in.AutoPublishOnAIApprove
	}
	if in.ReviewAccountID != nil {
		if *in.ReviewAccountID <= 0 {
			cur.ReviewAccountID = nil
		} else {
			var exists bool
			if qErr := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id = $1 AND deleted_at IS NULL)`, *in.ReviewAccountID).Scan(&exists); qErr != nil {
				return nil, qErr
			}
			if !exists {
				return nil, infraerrors.BadRequest("MARKET_REVIEW_ACCOUNT_NOT_FOUND", "所选审核上游账号不存在或已删除。")
			}
			id := *in.ReviewAccountID
			cur.ReviewAccountID = &id
		}
	}
	if in.ReviewModel != nil {
		cur.ReviewModel = strings.TrimSpace(*in.ReviewModel)
		if len(cur.ReviewModel) > 160 {
			return nil, infraerrors.BadRequest("MARKET_SETTING_INVALID", "审核模型名称过长。")
		}
	}
	if in.ReviewAPIBase != nil {
		cur.ReviewAPIBase = strings.TrimSpace(*in.ReviewAPIBase)
		if len(cur.ReviewAPIBase) > 512 {
			return nil, infraerrors.BadRequest("MARKET_SETTING_INVALID", "审核 API 地址过长。")
		}
	}
	if in.ClearReviewAPIKey {
		cur.reviewAPIKey = ""
	} else if in.ReviewAPIKey != nil && strings.TrimSpace(*in.ReviewAPIKey) != "" {
		cur.reviewAPIKey = strings.TrimSpace(*in.ReviewAPIKey)
	}
	if in.AttributionDays != nil {
		if *in.AttributionDays < 1 || *in.AttributionDays > 3650 {
			return nil, infraerrors.BadRequest("MARKET_SETTING_INVALID", "归因有效期必须在 1 到 3650 天之间。")
		}
		cur.AttributionDays = *in.AttributionDays
	}
	if in.FreezeDays != nil {
		if *in.FreezeDays < 0 || *in.FreezeDays > 180 {
			return nil, infraerrors.BadRequest("MARKET_SETTING_INVALID", "冻结天数必须在 0 到 180 天之间。")
		}
		cur.FreezeDays = *in.FreezeDays
	}
	if in.MinWithdrawAmount != nil {
		if *in.MinWithdrawAmount < 1 || *in.MinWithdrawAmount > 100000 {
			return nil, infraerrors.BadRequest("MARKET_SETTING_INVALID", "最低提现金额必须在 1 到 100000 之间。")
		}
		cur.MinWithdrawAmount = *in.MinWithdrawAmount
	}
	if in.TransferBonusPercent != nil {
		if *in.TransferBonusPercent < 0 || *in.TransferBonusPercent > 100 {
			return nil, infraerrors.BadRequest("MARKET_SETTING_INVALID", "转算力加成比例必须在 0% 到 100% 之间。")
		}
		cur.TransferBonusPercent = *in.TransferBonusPercent
	}
	checkRate := func(ptr *float64, target *float64, label string) error {
		if ptr == nil {
			return nil
		}
		if *ptr < 0 || *ptr > 50 {
			return infraerrors.BadRequest("MARKET_SETTING_INVALID", label+"返现比例必须在 0% 到 50% 之间。")
		}
		*target = *ptr
		return nil
	}
	if err := checkRate(in.RebateLLMPercent, &cur.RebateLLMPercent, "LLM"); err != nil {
		return nil, err
	}
	if err := checkRate(in.RebateImagePercent, &cur.RebateImagePercent, "图片"); err != nil {
		return nil, err
	}
	if err := checkRate(in.RebateVideoPercent, &cur.RebateVideoPercent, "视频"); err != nil {
		return nil, err
	}
	if err := checkRate(in.RebateMusicPercent, &cur.RebateMusicPercent, "音乐"); err != nil {
		return nil, err
	}

	var reviewAccParam any
	if cur.ReviewAccountID != nil && *cur.ReviewAccountID > 0 {
		reviewAccParam = *cur.ReviewAccountID
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE donebot_market_settings
		SET enabled = $1,
		    auto_publish_on_ai_approve = $2,
		    review_account_id = $3,
		    review_model = $4,
		    review_api_base = $5,
		    review_api_key = $6,
		    attribution_days = $7,
		    freeze_days = $8,
		    min_withdraw_amount = $9,
		    transfer_bonus_percent = $10,
		    rebate_llm_percent = $11,
		    rebate_image_percent = $12,
		    rebate_video_percent = $13,
		    rebate_music_percent = $14,
		    updated_at = NOW()
		WHERE id = 1`,
		cur.Enabled, cur.AutoPublishOnAIApprove, reviewAccParam, cur.ReviewModel, cur.ReviewAPIBase, cur.reviewAPIKey,
		cur.AttributionDays, cur.FreezeDays, cur.MinWithdrawAmount, cur.TransferBonusPercent,
		cur.RebateLLMPercent, cur.RebateImagePercent, cur.RebateVideoPercent, cur.RebateMusicPercent,
	)
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx)
}

// --- Categories ---

func (s *Service) ListCategories(ctx context.Context, includeDisabled bool) ([]Category, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	query := `
		SELECT c.id, c.slug, c.name, c.description, c.sort_order, c.enabled,
		       COUNT(l.id) FILTER (WHERE l.status = 'published') AS listing_count,
		       c.created_at, c.updated_at
		FROM donebot_market_categories c
		LEFT JOIN donebot_market_listings l ON l.category_id = c.id`
	if !includeDisabled {
		query += ` WHERE c.enabled = TRUE`
	}
	query += ` GROUP BY c.id ORDER BY c.sort_order ASC, c.id ASC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]Category, 0)
	for rows.Next() {
		var item Category
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Description, &item.SortOrder, &item.Enabled, &item.ListingCnt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) SaveCategory(ctx context.Context, in SaveCategoryInput) (*Category, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	name, err := cleanLine(in.Name, 60, "MARKET_CATEGORY_INVALID", "请填写有效的分类名称。")
	if err != nil {
		return nil, err
	}
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	if !slugPattern.MatchString(slug) {
		return nil, infraerrors.BadRequest("MARKET_CATEGORY_INVALID", "分类标识仅支持小写字母、数字和短横线。")
	}
	desc, err := cleanMultiLine(in.Description, 200, false, "MARKET_CATEGORY_INVALID", "分类简介过长。")
	if err != nil {
		return nil, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	var item Category
	if in.ID > 0 {
		err = s.db.QueryRowContext(ctx, `
			UPDATE donebot_market_categories
			SET slug = $1, name = $2, description = $3, sort_order = $4, enabled = $5, updated_at = NOW()
			WHERE id = $6
			RETURNING id, slug, name, description, sort_order, enabled, created_at, updated_at`,
			slug, name, desc, in.SortOrder, enabled, in.ID).Scan(
			&item.ID, &item.Slug, &item.Name, &item.Description, &item.SortOrder, &item.Enabled, &item.CreatedAt, &item.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("MARKET_CATEGORY_NOT_FOUND", "分类不存在。")
		}
	} else {
		err = s.db.QueryRowContext(ctx, `
			INSERT INTO donebot_market_categories (slug, name, description, sort_order, enabled)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, slug, name, description, sort_order, enabled, created_at, updated_at`,
			slug, name, desc, in.SortOrder, enabled).Scan(
			&item.ID, &item.Slug, &item.Name, &item.Description, &item.SortOrder, &item.Enabled, &item.CreatedAt, &item.UpdatedAt)
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, infraerrors.Conflict("MARKET_CATEGORY_EXISTS", "分类标识已存在，请更换。")
		}
		return nil, err
	}
	return &item, nil
}

func (s *Service) DeleteCategory(ctx context.Context, id int64) error {
	if err := s.available(); err != nil {
		return err
	}
	var used int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_market_listings WHERE category_id = $1`, id).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return infraerrors.Conflict("MARKET_CATEGORY_IN_USE", "该分类下已有 Bot，无法删除；可以先设为停用。")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM donebot_market_categories WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return infraerrors.NotFound("MARKET_CATEGORY_NOT_FOUND", "分类不存在。")
	}
	return nil
}

// --- Publish / Update Bot ---

func (s *Service) PublishBot(ctx context.Context, authorUserID int64, in PublishBotInput) (*ListingDetail, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return nil, infraerrors.Forbidden("BOT_MARKET_DISABLED", "Bot 广场暂未开放。")
	}

	name, err := cleanLine(in.Name, 80, "MARKET_NAME_INVALID", "请填写 Bot 名称（最多 80 字）。")
	if err != nil {
		return nil, err
	}
	tagline, err := cleanLine(in.Tagline, 160, "MARKET_TAGLINE_INVALID", "请填写一句话简介（最多 160 字）。")
	if err != nil {
		return nil, err
	}
	desc, err := cleanMultiLine(in.Description, 4000, false, "MARKET_DESC_INVALID", "详细介绍最多 4000 字。")
	if err != nil {
		return nil, err
	}
	soulText, err := cleanMultiLine(in.SoulText, MaxSoulRunes, true, "MARKET_SOUL_INVALID", "请提供完整的 SOUL.md 设定内容。")
	if err != nil {
		return nil, err
	}
	changelog, err := cleanMultiLine(in.Changelog, 1000, false, "MARKET_CHANGELOG_INVALID", "更新说明过长。")
	if err != nil {
		return nil, err
	}

	// Verify enabled category exists.
	var catEnabled bool
	var catName string
	err = s.db.QueryRowContext(ctx, `SELECT name, enabled FROM donebot_market_categories WHERE id = $1`, in.CategoryID).Scan(&catName, &catEnabled)
	if errors.Is(err, sql.ErrNoRows) || !catEnabled {
		return nil, infraerrors.BadRequest("MARKET_CATEGORY_INVALID", "请选择有效的 Bot 分类。")
	}
	if err != nil {
		return nil, err
	}

	// Validate package JSON.
	if len(in.Package) == 0 || len(in.Package) > MaxPackageBytes || !json.Valid(in.Package) {
		return nil, infraerrors.BadRequest("MARKET_PACKAGE_INVALID", "Bot 模板包必须是有效的 JSON 且不超过 4 MB。")
	}
	sum := sha256.Sum256(in.Package)
	pkgHash := hex.EncodeToString(sum[:])

	// Clean tags, prompts, skills.
	tags := make([]string, 0, MaxTagsPerListing)
	tagSeen := map[string]struct{}{}
	for _, raw := range in.Tags {
		t := strings.TrimSpace(raw)
		if t == "" || utf8.RuneCountInString(t) > 24 {
			continue
		}
		if _, dup := tagSeen[t]; dup {
			continue
		}
		tagSeen[t] = struct{}{}
		tags = append(tags, t)
		if len(tags) >= MaxTagsPerListing {
			break
		}
	}
	prompts := make([]string, 0, MaxSamplePrompts)
	for _, raw := range in.SamplePrompts {
		p := strings.TrimSpace(raw)
		if p == "" || utf8.RuneCountInString(p) > 300 {
			continue
		}
		prompts = append(prompts, p)
		if len(prompts) >= MaxSamplePrompts {
			break
		}
	}
	listingType := strings.ToLower(strings.TrimSpace(in.ListingType))
	if listingType == "" {
		listingType = "bot"
	}
	if listingType != "bot" && listingType != "studio" {
		return nil, infraerrors.BadRequest("MARKET_TYPE_INVALID", "发布类型仅支持单 Bot（bot）或 AI 工作室（studio）。")
	}
	costTier := strings.ToLower(strings.TrimSpace(in.CostTier))
	if costTier == "" {
		costTier = "standard"
	}
	if costTier != "light" && costTier != "standard" && costTier != "heavy" {
		return nil, infraerrors.BadRequest("MARKET_COST_TIER_INVALID", "算力消耗档位仅支持 light、standard 或 heavy。")
	}

	botsSummary := make([]StudioBotSummary, 0, len(in.BotsSummary))
	for _, b := range in.BotsSummary {
		bn := strings.TrimSpace(b.Name)
		if bn == "" || utf8.RuneCountInString(bn) > 60 {
			continue
		}
		br := strings.TrimSpace(b.Role)
		if utf8.RuneCountInString(br) > 120 {
			br = truncateSnippet(br, 120)
		}
		ba := strings.TrimSpace(b.Avatar)
		if len(ba) > 256 {
			ba = ""
		}
		bc := strings.TrimSpace(b.Color)
		if len(bc) > 32 {
			bc = ""
		}
		sc := b.SkillCount
		if sc < 0 {
			sc = 0
		}
		botsSummary = append(botsSummary, StudioBotSummary{
			Name: bn, Role: br, Avatar: ba, Color: bc, SkillCount: sc,
		})
		if len(botsSummary) >= MaxBotsPerStudio {
			break
		}
	}
	botCount := 1
	if listingType == "studio" {
		if len(botsSummary) < 2 {
			return nil, infraerrors.BadRequest("MARKET_STUDIO_MIN_BOTS", "AI 工作室整包至少需要包含 2 位 AI 员工。")
		}
		botCount = len(botsSummary)
	}

	evalSamples := make([]EvalSample, 0, MaxEvalSamples)
	for _, es := range in.EvalSamples {
		pr := strings.TrimSpace(es.Prompt)
		op := strings.TrimSpace(es.OutputPreview)
		if pr == "" || op == "" {
			continue
		}
		evalSamples = append(evalSamples, EvalSample{
			Title:         truncateSnippet(es.Title, 80),
			Prompt:        truncateSnippet(pr, 400),
			OutputPreview: truncateSnippet(op, 1200),
			Model:         truncateSnippet(es.Model, 80),
			LatencyMs:     es.LatencyMs,
			VerifiedAt:    truncateSnippet(es.VerifiedAt, 40),
		})
		if len(evalSamples) >= MaxEvalSamples {
			break
		}
	}
	evalVerified := in.EvalVerified && len(evalSamples) > 0

	maxSkills := MaxSkillsPerBot
	if listingType == "studio" {
		maxSkills = MaxSkillsPerStudio
	}
	if len(in.Skills) > maxSkills {
		return nil, infraerrors.BadRequest("MARKET_SKILLS_TOO_MANY", fmt.Sprintf("最多附带 %d 个 Skill。", maxSkills))
	}
	skills := make([]SkillDocument, 0, len(in.Skills))
	skillNames := make([]string, 0, len(in.Skills))
	for _, sk := range in.Skills {
		sn, sErr := cleanLine(sk.Name, 80, "MARKET_SKILL_INVALID", "Skill 名称无效。")
		if sErr != nil {
			return nil, sErr
		}
		sd, sErr := cleanMultiLine(sk.Description, MaxSkillDescRunes, false, "MARKET_SKILL_INVALID", fmt.Sprintf("Skill「%s」简介过长（最多 %d 字）。", sn, MaxSkillDescRunes))
		if sErr != nil {
			return nil, sErr
		}
		si, sErr := cleanMultiLine(sk.Instructions, MaxSkillInstrRunes, true, "MARKET_SKILL_INVALID", fmt.Sprintf("Skill「%s」内容不能为空或超过 %d 字。", sn, MaxSkillInstrRunes))
		if sErr != nil {
			return nil, sErr
		}
		skills = append(skills, SkillDocument{Name: sn, Description: sd, Instructions: si})
		skillNames = append(skillNames, sn)
	}

	appearance := in.Appearance
	if len(appearance) == 0 || !json.Valid(appearance) || len(appearance) > 128*1024 {
		appearance = json.RawMessage(`{}`)
	}

	// Synchronous hard-rule pre-check: fast immediate rejection for secrets/obvious attacks/empty SOUL/department hazards.
	if hardReject := RunHardRuleCheck(ReviewInput{
		ListingType: listingType, BotsSummary: botsSummary,
		Name: name, Tagline: tagline, Description: desc, CategoryName: catName,
		SamplePrompts: prompts, SoulText: soulText, Skills: skills, PackageJSON: in.Package,
	}); hardReject != nil {
		return nil, infraerrors.BadRequest("BOT_REVIEW_REJECTED", strings.Join(hardReject.RejectionReasons, "；"))
	}

	tagsJSON, _ := json.Marshal(tags)
	promptsJSON, _ := json.Marshal(prompts)
	skillsJSON, _ := json.Marshal(skills)
	skillNamesJSON, _ := json.Marshal(skillNames)
	botsSummaryJSON, _ := json.Marshal(botsSummary)
	evalSamplesJSON, _ := json.Marshal(evalSamples)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	listingID := strings.TrimSpace(in.ListingID)
	nextVersion := 1
	if listingID != "" {
		if !validUUID(listingID) {
			return nil, infraerrors.BadRequest("MARKET_LISTING_INVALID", "无效的 Bot 广场 ID。")
		}
		var ownerID int64
		var curStatus string
		err = tx.QueryRowContext(ctx, `
			SELECT author_user_id, latest_version, status
			FROM donebot_market_listings WHERE id = $1 FOR UPDATE`, listingID).Scan(&ownerID, &nextVersion, &curStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("MARKET_LISTING_NOT_FOUND", "Bot 条目不存在。")
		}
		if err != nil {
			return nil, err
		}
		if ownerID != authorUserID {
			return nil, infraerrors.Forbidden("MARKET_FORBIDDEN", "只能更新自己发布的 Bot。")
		}
		if curStatus == "suspended" {
			return nil, infraerrors.Forbidden("MARKET_SUSPENDED", "该 Bot 已被管理员封禁，无法重新提交。")
		}
		nextVersion++
	} else {
		listingID = uuid.NewString()
	}

	releaseID := uuid.NewString()
	if nextVersion == 1 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO donebot_market_listings (
				id, author_user_id, category_id, source_bot_id, listing_type, bot_count, bots_summary_json,
				eval_verified, eval_samples_json, cost_tier, name, tagline, description,
				tags_json, sample_prompts_json, appearance_json, has_soul, skill_count, skill_names_json,
				latest_release_id, latest_version, status, last_review_note
			) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9::jsonb,$10,$11,$12,$13,$14::jsonb,$15::jsonb,$16::jsonb,TRUE,$17,$18::jsonb,$19,1,'pending_review','')`,
			listingID, authorUserID, in.CategoryID, strings.TrimSpace(in.SourceBotID), listingType, botCount, string(botsSummaryJSON),
			evalVerified, string(evalSamplesJSON), costTier, name, tagline, desc,
			string(tagsJSON), string(promptsJSON), string(appearance), len(skills), string(skillNamesJSON), releaseID); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE donebot_market_listings
			SET category_id = $1, listing_type = $2, bot_count = $3, bots_summary_json = $4::jsonb,
			    eval_verified = $5, eval_samples_json = $6::jsonb, cost_tier = $7,
			    name = $8, tagline = $9, description = $10,
			    tags_json = $11::jsonb, sample_prompts_json = $12::jsonb, appearance_json = $13::jsonb,
			    has_soul = TRUE, skill_count = $14, skill_names_json = $15::jsonb,
			    latest_release_id = $16, latest_version = $17,
			    status = CASE WHEN published_release_id IS NOT NULL AND status = 'published' THEN 'published' ELSE 'pending_review' END,
			    last_review_note = '',
			    updated_at = NOW()
			WHERE id = $18`,
			in.CategoryID, listingType, botCount, string(botsSummaryJSON),
			evalVerified, string(evalSamplesJSON), costTier,
			name, tagline, desc, string(tagsJSON), string(promptsJSON), string(appearance),
			len(skills), string(skillNamesJSON), releaseID, nextVersion, listingID); err != nil {
			return nil, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO donebot_market_releases (
			id, listing_id, version, changelog, soul_text, skills_json, package_json, package_sha256, package_bytes, review_status
		) VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8,$9,'pending')`,
		releaseID, listingID, nextVersion, changelog, soulText, string(skillsJSON), string(in.Package), pkgHash, len(in.Package)); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.kickReviewWorker()
	return s.GetListingDetail(ctx, listingID, authorUserID, false)
}

// --- Browse / Detail / MyListings / Unlist ---

func (s *Service) ListMarketplace(ctx context.Context, filter MarketplaceFilter) ([]ListingSummary, int, error) {
	if err := s.available(); err != nil {
		return nil, 0, err
	}
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 || filter.PageSize > 50 {
		filter.PageSize = 20
	}

	where := []string{"l.status = 'published'", "l.published_release_id IS NOT NULL", "c.enabled = TRUE"}
	args := make([]any, 0)
	argIdx := 1

	if filter.CategoryID > 0 {
		where = append(where, fmt.Sprintf("l.category_id = $%d", argIdx))
		args = append(args, filter.CategoryID)
		argIdx++
	}
	if lt := strings.ToLower(strings.TrimSpace(filter.ListingType)); lt == "bot" || lt == "studio" {
		where = append(where, fmt.Sprintf("l.listing_type = $%d", argIdx))
		args = append(args, lt)
		argIdx++
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		like := "%" + q + "%"
		where = append(where, fmt.Sprintf("(l.name ILIKE $%d OR l.tagline ILIKE $%d OR l.description ILIKE $%d OR l.tags_json::text ILIKE $%d)", argIdx, argIdx, argIdx, argIdx))
		args = append(args, like)
		argIdx++
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM donebot_market_listings l
		JOIN donebot_market_categories c ON c.id = l.category_id
		WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	orderSQL := "l.featured DESC, l.install_count DESC, l.total_attributed_cost DESC, l.published_at DESC"
	switch filter.Sort {
	case "newest", "latest":
		orderSQL = "l.published_at DESC, l.created_at DESC"
	case "installs", "popular":
		orderSQL = "l.install_count DESC, l.published_at DESC"
	case "active":
		orderSQL = "l.total_attributed_cost DESC, l.install_count DESC, l.published_at DESC"
	}

	offset := (filter.Page - 1) * filter.PageSize
	listArgs := append(args, filter.PageSize, offset)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT l.id, l.listing_type, l.bot_count, l.bots_summary_json, l.eval_verified, l.eval_samples_json, l.cost_tier,
		       l.author_user_id, COALESCE(NULLIF(u.username, ''), SPLIT_PART(u.email, '@', 1)), COALESCE(ua.url, ''),
		       l.category_id, c.name, c.slug, l.source_bot_id, l.name, l.tagline, l.description,
		       l.tags_json, l.sample_prompts_json, l.appearance_json,
		       l.has_soul, l.skill_count, l.skill_names_json,
		       COALESCE(l.latest_release_id::text, ''), COALESCE(l.published_release_id::text, ''),
		       l.latest_version, l.published_version, l.status, l.featured, l.install_count,
		       l.published_at, l.created_at, l.updated_at
		FROM donebot_market_listings l
		JOIN donebot_market_categories c ON c.id = l.category_id
		JOIN users u ON u.id = l.author_user_id
		LEFT JOIN user_avatars ua ON ua.user_id = u.id
		WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d`, whereSQL, orderSQL, argIdx, argIdx+1), listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]ListingSummary, 0)
	for rows.Next() {
		item, err := scanPublicListing(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func scanPublicListing(scanner interface{ Scan(...any) error }) (ListingSummary, error) {
	var (
		item                                                    ListingSummary
		botsSummaryRaw, evalSamplesRaw                          []byte
		tagsRaw, promptsRaw, appRaw, snRaw                      []byte
	)
	err := scanner.Scan(
		&item.ID, &item.ListingType, &item.BotCount, &botsSummaryRaw, &item.EvalVerified, &evalSamplesRaw, &item.CostTier,
		&item.AuthorUserID, &item.AuthorName, &item.AuthorAvatarURL,
		&item.CategoryID, &item.CategoryName, &item.CategorySlug, &item.SourceBotID,
		&item.Name, &item.Tagline, &item.Description,
		&tagsRaw, &promptsRaw, &appRaw,
		&item.HasSoul, &item.SkillCount, &snRaw,
		&item.LatestReleaseID, &item.PublishedReleaseID,
		&item.LatestVersion, &item.PublishedVersion, &item.Status, &item.Featured, &item.InstallCount,
		&item.PublishedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return item, err
	}
	_ = json.Unmarshal(botsSummaryRaw, &item.BotsSummary)
	_ = json.Unmarshal(evalSamplesRaw, &item.EvalSamples)
	_ = json.Unmarshal(tagsRaw, &item.Tags)
	_ = json.Unmarshal(promptsRaw, &item.SamplePrompts)
	_ = json.Unmarshal(snRaw, &item.SkillNames)
	item.Appearance = appRaw
	if item.ListingType == "" {
		item.ListingType = "bot"
	}
	if item.BotCount <= 0 {
		item.BotCount = 1
	}
	if item.CostTier == "" {
		item.CostTier = "standard"
	}
	if item.BotsSummary == nil {
		item.BotsSummary = []StudioBotSummary{}
	}
	if item.EvalSamples == nil {
		item.EvalSamples = []EvalSample{}
	}
	if item.Tags == nil {
		item.Tags = []string{}
	}
	if item.SamplePrompts == nil {
		item.SamplePrompts = []string{}
	}
	if item.SkillNames == nil {
		item.SkillNames = []string{}
	}
	return item, nil
}

func (s *Service) GetListingDetail(ctx context.Context, listingID string, viewerUserID int64, isAdmin bool) (*ListingDetail, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if !validUUID(listingID) {
		return nil, infraerrors.BadRequest("MARKET_LISTING_INVALID", "无效的 Bot ID。")
	}
	var (
		item                               ListingSummary
		botsSummaryRaw, evalSamplesRaw     []byte
		tagsRaw, promptsRaw, appRaw, snRaw []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT l.id, l.listing_type, l.bot_count, l.bots_summary_json, l.eval_verified, l.eval_samples_json, l.cost_tier,
		       l.author_user_id, COALESCE(NULLIF(u.username, ''), SPLIT_PART(u.email, '@', 1)), COALESCE(ua.url, ''),
		       l.category_id, c.name, c.slug, l.source_bot_id, l.name, l.tagline, l.description,
		       l.tags_json, l.sample_prompts_json, l.appearance_json,
		       l.has_soul, l.skill_count, l.skill_names_json,
		       COALESCE(l.latest_release_id::text, ''), COALESCE(l.published_release_id::text, ''),
		       l.latest_version, l.published_version, l.status, l.featured, l.install_count,
		       l.total_attributed_cost, l.total_creator_reward, l.report_count, l.last_review_note,
		       l.published_at, l.created_at, l.updated_at
		FROM donebot_market_listings l
		JOIN donebot_market_categories c ON c.id = l.category_id
		JOIN users u ON u.id = l.author_user_id
		LEFT JOIN user_avatars ua ON ua.user_id = u.id
		WHERE l.id = $1`, listingID).Scan(
		&item.ID, &item.ListingType, &item.BotCount, &botsSummaryRaw, &item.EvalVerified, &evalSamplesRaw, &item.CostTier,
		&item.AuthorUserID, &item.AuthorName, &item.AuthorAvatarURL,
		&item.CategoryID, &item.CategoryName, &item.CategorySlug, &item.SourceBotID,
		&item.Name, &item.Tagline, &item.Description,
		&tagsRaw, &promptsRaw, &appRaw,
		&item.HasSoul, &item.SkillCount, &snRaw,
		&item.LatestReleaseID, &item.PublishedReleaseID,
		&item.LatestVersion, &item.PublishedVersion, &item.Status, &item.Featured, &item.InstallCount,
		&item.TotalAttributedCost, &item.TotalCreatorReward, &item.ReportCount, &item.LastReviewNote,
		&item.PublishedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("MARKET_LISTING_NOT_FOUND", "Bot 不存在。")
	}
	if err != nil {
		return nil, err
	}
	canSeePrivate := isAdmin || (viewerUserID > 0 && viewerUserID == item.AuthorUserID)
	if item.Status != "published" && !canSeePrivate {
		return nil, infraerrors.NotFound("MARKET_LISTING_NOT_FOUND", "Bot 不存在或已下架。")
	}
	_ = json.Unmarshal(botsSummaryRaw, &item.BotsSummary)
	_ = json.Unmarshal(evalSamplesRaw, &item.EvalSamples)
	_ = json.Unmarshal(tagsRaw, &item.Tags)
	_ = json.Unmarshal(promptsRaw, &item.SamplePrompts)
	_ = json.Unmarshal(snRaw, &item.SkillNames)
	item.Appearance = appRaw
	if item.ListingType == "" {
		item.ListingType = "bot"
	}
	if item.BotCount <= 0 {
		item.BotCount = 1
	}
	if item.CostTier == "" {
		item.CostTier = "standard"
	}
	if item.BotsSummary == nil {
		item.BotsSummary = []StudioBotSummary{}
	}
	if item.EvalSamples == nil {
		item.EvalSamples = []EvalSample{}
	}
	if item.Tags == nil {
		item.Tags = []string{}
	}
	if item.SamplePrompts == nil {
		item.SamplePrompts = []string{}
	}
	if item.SkillNames == nil {
		item.SkillNames = []string{}
	}
	if !canSeePrivate {
		item.TotalAttributedCost = 0
		item.TotalCreatorReward = 0
		item.ReportCount = 0
		item.LastReviewNote = ""
	} else if !isAdmin && item.Status == "published" {
		item.LastReviewNote = ""
	}

	releaseID := item.PublishedReleaseID
	if canSeePrivate && item.LatestReleaseID != "" {
		releaseID = item.LatestReleaseID
	}
	detail := &ListingDetail{ListingSummary: item, Skills: []SkillDocument{}}
	if releaseID != "" {
		rel, rErr := s.loadRelease(ctx, releaseID, canSeePrivate)
		if rErr == nil && rel != nil {
			detail.SoulPreview = truncateSnippet(rel.SoulText, 1200)
			detail.Skills = rel.Skills
			if canSeePrivate {
				if !isAdmin && item.Status == "published" && rel.ReviewStatus == "approved" {
					rel.ReviewNote = ""
					rel.AIReport = nil
				}
				detail.LatestRelease = rel
			}
		}
	}
	return detail, nil
}

func (s *Service) loadRelease(ctx context.Context, releaseID string, includePackage bool) (*ReleaseDetail, error) {
	var (
		rel                 ReleaseDetail
		skillsRaw, pkgRaw   []byte
		reportRaw           []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, listing_id, version, changelog, soul_text, skills_json, package_json,
		       package_sha256, package_bytes, review_status, ai_verdict, ai_risk_score,
		       ai_report_json, review_note, reviewed_at, created_at
		FROM donebot_market_releases WHERE id = $1`, releaseID).Scan(
		&rel.ID, &rel.ListingID, &rel.Version, &rel.Changelog, &rel.SoulText, &skillsRaw, &pkgRaw,
		&rel.PackageSHA256, &rel.PackageBytes, &rel.ReviewStatus, &rel.AIVerdict, &rel.AIRiskScore,
		&reportRaw, &rel.ReviewNote, &rel.ReviewedAt, &rel.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(skillsRaw, &rel.Skills)
	if rel.Skills == nil {
		rel.Skills = []SkillDocument{}
	}
	if includePackage {
		rel.Package = pkgRaw
	}
	if len(reportRaw) > 2 {
		var rep AIReviewReport
		if json.Unmarshal(reportRaw, &rep) == nil && (rep.Verdict != "" || rep.Summary != "") {
			rel.AIReport = &rep
		}
	}
	return &rel, nil
}

func (s *Service) ListMyListings(ctx context.Context, authorUserID int64) ([]ListingDetail, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM donebot_market_listings
		WHERE author_user_id = $1
		ORDER BY updated_at DESC
		LIMIT 100`, authorUserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ListingDetail, 0, len(ids))
	for _, id := range ids {
		d, dErr := s.GetListingDetail(ctx, id, authorUserID, false)
		if dErr == nil && d != nil {
			out = append(out, *d)
		}
	}
	return out, nil
}

func (s *Service) SetAuthorListingVisibility(ctx context.Context, authorUserID int64, listingID string, listed bool) error {
	if err := s.available(); err != nil {
		return err
	}
	if !validUUID(listingID) {
		return infraerrors.BadRequest("MARKET_LISTING_INVALID", "无效的 Bot ID。")
	}
	var status string
	var publishedRelease sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT status, published_release_id::text
		FROM donebot_market_listings WHERE id = $1 AND author_user_id = $2`, listingID, authorUserID).Scan(&status, &publishedRelease)
	if errors.Is(err, sql.ErrNoRows) {
		return infraerrors.NotFound("MARKET_LISTING_NOT_FOUND", "Bot 不存在。")
	}
	if err != nil {
		return err
	}
	if status == "suspended" {
		return infraerrors.Forbidden("MARKET_SUSPENDED", "该 Bot 已被管理员封禁。")
	}
	if !listed {
		if status == "published" || (publishedRelease.Valid && publishedRelease.String != "") {
			return infraerrors.Forbidden("MARKET_UNLIST_FORBIDDEN", "已审核通过并上架的 Bot 或部门不支持自助下架，如需处理请联系管理员。")
		}
		return infraerrors.BadRequest("MARKET_NOT_PUBLISHED", "当前条目尚未上架，无需下架。")
	}
	if !publishedRelease.Valid || publishedRelease.String == "" {
		return infraerrors.BadRequest("MARKET_NOT_APPROVED", "该 Bot 尚未通过审核，无法上架。")
	}
	_, err = s.db.ExecContext(ctx, `UPDATE donebot_market_listings SET status = 'published', updated_at = NOW() WHERE id = $1`, listingID)
	return err
}

// --- Install & Signed Attribution Token ---

func (s *Service) InstallListing(ctx context.Context, installerUserID int64, listingID, installationID string) (*InstallResult, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if !validUUID(listingID) {
		return nil, infraerrors.BadRequest("MARKET_LISTING_INVALID", "无效的 Bot ID。")
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return nil, infraerrors.Forbidden("BOT_MARKET_DISABLED", "Bot 广场暂未开放。")
	}

	var (
		authorUserID     int64
		listingType      string
		botCount         int
		botsSummaryRaw   []byte
		name             string
		promptsRaw       []byte
		releaseID        string
		publishedVersion int
		status           string
	)
	err = s.db.QueryRowContext(ctx, `
		SELECT author_user_id, listing_type, bot_count, bots_summary_json, name, sample_prompts_json, COALESCE(published_release_id::text, ''), published_version, status
		FROM donebot_market_listings WHERE id = $1`, listingID).Scan(
		&authorUserID, &listingType, &botCount, &botsSummaryRaw, &name, &promptsRaw, &releaseID, &publishedVersion, &status)
	if errors.Is(err, sql.ErrNoRows) || status != "published" || releaseID == "" {
		return nil, infraerrors.NotFound("MARKET_LISTING_NOT_FOUND", "该 Bot 不存在或已下架。")
	}
	if err != nil {
		return nil, err
	}

	rel, err := s.loadRelease(ctx, releaseID, true)
	if err != nil {
		return nil, err
	}

	installID := uuid.NewString()
	expiresAt := time.Now().UTC().Add(time.Duration(settings.AttributionDays) * 24 * time.Hour)
	selfInstall := authorUserID == installerUserID
	installStatus := "active"
	if selfInstall {
		// Self-installs never accrue creator rebates or inflate marketplace install counts.
		installStatus = "revoked"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO donebot_market_installs (
			id, listing_id, release_id, release_version, author_user_id, installer_user_id, installation_id, status, expires_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		installID, listingID, releaseID, publishedVersion, authorUserID, installerUserID, strings.TrimSpace(installationID), installStatus, expiresAt); err != nil {
		return nil, err
	}
	if !selfInstall {
		if _, err := tx.ExecContext(ctx, `UPDATE donebot_market_listings SET install_count = install_count + 1, updated_at = NOW() WHERE id = $1`, listingID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	token, err := SignInstallToken(settings.signingSecret, InstallTokenClaims{
		InstallID:       installID,
		ListingID:       listingID,
		AuthorUserID:    authorUserID,
		InstallerUserID: installerUserID,
		ExpiresAt:       expiresAt.Unix(),
	})
	if err != nil {
		return nil, err
	}
	var prompts []string
	_ = json.Unmarshal(promptsRaw, &prompts)
	var botsSummary []StudioBotSummary
	_ = json.Unmarshal(botsSummaryRaw, &botsSummary)
	if botsSummary == nil {
		botsSummary = []StudioBotSummary{}
	}
	if listingType == "" {
		listingType = "bot"
	}
	if botCount <= 0 {
		botCount = 1
	}

	return &InstallResult{
		InstallID:     installID,
		ListingID:     listingID,
		ListingType:   listingType,
		BotCount:      botCount,
		BotsSummary:   botsSummary,
		ReleaseID:     releaseID,
		Version:       publishedVersion,
		Name:          name,
		InstallToken:  token,
		SelfInstall:   selfInstall,
		ExpiresAt:     expiresAt,
		Package:       rel.Package,
		Skills:        rel.Skills,
		SoulText:      rel.SoulText,
		SamplePrompts: prompts,
	}, nil
}

// --- Reports ---

func (s *Service) ReportListing(ctx context.Context, reporterUserID int64, listingID string, in ReportListingInput) error {
	if err := s.available(); err != nil {
		return err
	}
	if !validUUID(listingID) {
		return infraerrors.BadRequest("MARKET_LISTING_INVALID", "无效的 Bot ID。")
	}
	reason := normalizeReportReason(in.ReasonType)
	if reason == "" {
		return infraerrors.BadRequest("MARKET_REPORT_INVALID", "请选择有效的举报原因。")
	}
	detail, err := cleanMultiLine(in.Detail, 2000, false, "MARKET_REPORT_INVALID", "举报详情过长。")
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO donebot_market_reports (listing_id, reporter_user_id, reason_type, detail)
		VALUES ($1,$2,$3,$4)`, listingID, reporterUserID, reason, detail); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE donebot_market_listings SET report_count = report_count + 1 WHERE id = $1`, listingID); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Admin Moderation ---

func (s *Service) AdminOverview(ctx context.Context) (*AdminOverview, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	var out AdminOverview
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'published'),
		       COUNT(*) FILTER (WHERE status IN ('pending_review', 'manual_review')),
		       COALESCE(SUM(install_count), 0),
		       COALESCE(SUM(total_attributed_cost), 0),
		       COALESCE(SUM(total_creator_reward), 0)
		FROM donebot_market_listings`).Scan(
		&out.TotalListings, &out.PublishedListings, &out.PendingReviewCount,
		&out.TotalInstalls, &out.TotalAttributedCost, &out.TotalCreatorReward)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_market_reports WHERE status = 'open'`).Scan(&out.OpenReportCount)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_creator_withdrawals WHERE status = 'pending'`).Scan(&out.PendingWithdrawals)
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount), 0) FROM donebot_creator_withdrawals WHERE status = 'paid'`).Scan(&out.TotalWithdrawn)
	return &out, nil
}

func (s *Service) AdminListListings(ctx context.Context, status, query string, page, pageSize int) ([]ListingSummary, int, error) {
	if err := s.available(); err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	where := []string{"1=1"}
	args := make([]any, 0)
	idx := 1
	if st := strings.TrimSpace(status); st != "" && st != "all" {
		if st == "review_queue" {
			where = append(where, "l.status IN ('pending_review', 'manual_review')")
		} else {
			where = append(where, fmt.Sprintf("l.status = $%d", idx))
			args = append(args, st)
			idx++
		}
	}
	if q := strings.TrimSpace(query); q != "" {
		where = append(where, fmt.Sprintf("(l.name ILIKE $%d OR l.tagline ILIKE $%d OR u.email ILIKE $%d)", idx, idx, idx))
		args = append(args, "%"+q+"%")
		idx++
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM donebot_market_listings l
		JOIN users u ON u.id = l.author_user_id
		WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT l.id, l.listing_type, l.bot_count, l.bots_summary_json, l.eval_verified, l.eval_samples_json, l.cost_tier,
		       l.author_user_id, COALESCE(NULLIF(u.username, ''), u.email), COALESCE(ua.url, ''),
		       l.category_id, c.name, c.slug, l.source_bot_id, l.name, l.tagline, l.description,
		       l.tags_json, l.sample_prompts_json, l.appearance_json,
		       l.has_soul, l.skill_count, l.skill_names_json,
		       COALESCE(l.latest_release_id::text, ''), COALESCE(l.published_release_id::text, ''),
		       l.latest_version, l.published_version, l.status, l.featured, l.install_count,
		       l.total_attributed_cost, l.total_creator_reward, l.report_count, l.last_review_note,
		       l.published_at, l.created_at, l.updated_at
		FROM donebot_market_listings l
		JOIN donebot_market_categories c ON c.id = l.category_id
		JOIN users u ON u.id = l.author_user_id
		LEFT JOIN user_avatars ua ON ua.user_id = u.id
		WHERE %s
		ORDER BY CASE WHEN l.status IN ('manual_review', 'pending_review') THEN 0 ELSE 1 END, l.updated_at DESC
		LIMIT $%d OFFSET $%d`, whereSQL, idx, idx+1), append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]ListingSummary, 0)
	for rows.Next() {
		var (
			item                               ListingSummary
			botsSummaryRaw, evalSamplesRaw     []byte
			tagsRaw, promptsRaw, appRaw, snRaw []byte
		)
		if err := rows.Scan(
			&item.ID, &item.ListingType, &item.BotCount, &botsSummaryRaw, &item.EvalVerified, &evalSamplesRaw, &item.CostTier,
			&item.AuthorUserID, &item.AuthorName, &item.AuthorAvatarURL,
			&item.CategoryID, &item.CategoryName, &item.CategorySlug, &item.SourceBotID,
			&item.Name, &item.Tagline, &item.Description,
			&tagsRaw, &promptsRaw, &appRaw,
			&item.HasSoul, &item.SkillCount, &snRaw,
			&item.LatestReleaseID, &item.PublishedReleaseID,
			&item.LatestVersion, &item.PublishedVersion, &item.Status, &item.Featured, &item.InstallCount,
			&item.TotalAttributedCost, &item.TotalCreatorReward, &item.ReportCount, &item.LastReviewNote,
			&item.PublishedAt, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}
		_ = json.Unmarshal(botsSummaryRaw, &item.BotsSummary)
		_ = json.Unmarshal(evalSamplesRaw, &item.EvalSamples)
		_ = json.Unmarshal(tagsRaw, &item.Tags)
		_ = json.Unmarshal(promptsRaw, &item.SamplePrompts)
		_ = json.Unmarshal(snRaw, &item.SkillNames)
		item.Appearance = appRaw
		if item.ListingType == "" {
			item.ListingType = "bot"
		}
		if item.BotCount <= 0 {
			item.BotCount = 1
		}
		if item.CostTier == "" {
			item.CostTier = "standard"
		}
		if item.BotsSummary == nil {
			item.BotsSummary = []StudioBotSummary{}
		}
		if item.EvalSamples == nil {
			item.EvalSamples = []EvalSample{}
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Service) AdminReviewListing(ctx context.Context, adminUserID int64, listingID, decision, note string, featured *bool) (*ListingDetail, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if !validUUID(listingID) {
		return nil, infraerrors.BadRequest("MARKET_LISTING_INVALID", "无效的 Bot ID。")
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	note = strings.TrimSpace(note)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var latestRelID sql.NullString
	var latestVer int
	err = tx.QueryRowContext(ctx, `SELECT latest_release_id::text, latest_version FROM donebot_market_listings WHERE id = $1 FOR UPDATE`, listingID).Scan(&latestRelID, &latestVer)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("MARKET_LISTING_NOT_FOUND", "Bot 不存在。")
	}
	if err != nil {
		return nil, err
	}

	switch decision {
	case "approve":
		if !latestRelID.Valid || latestRelID.String == "" {
			return nil, infraerrors.BadRequest("MARKET_NO_RELEASE", "缺少可审核版本。")
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE donebot_market_releases
			SET review_status = 'approved', reviewed_by = $1, review_note = $2, reviewed_at = NOW()
			WHERE id = $3`, adminUserID, note, latestRelID.String); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE donebot_market_listings
			SET status = 'published', published_release_id = $1, published_version = $2,
			    last_review_note = $3, published_at = COALESCE(published_at, NOW()), updated_at = NOW()
			WHERE id = $4`, latestRelID.String, latestVer, note, listingID); err != nil {
			return nil, err
		}
	case "reject":
		if note == "" {
			note = "人工复核未通过，请按平台内容与安全规范调整后重新提交。"
		}
		if latestRelID.Valid && latestRelID.String != "" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE donebot_market_releases
				SET review_status = 'rejected', reviewed_by = $1, review_note = $2, reviewed_at = NOW()
				WHERE id = $3`, adminUserID, note, latestRelID.String); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE donebot_market_listings
			SET status = CASE WHEN published_release_id IS NOT NULL AND status = 'published' THEN 'published' ELSE 'rejected' END,
			    last_review_note = $1, updated_at = NOW()
			WHERE id = $2`, note, listingID); err != nil {
			return nil, err
		}
	case "suspend":
		if _, err := tx.ExecContext(ctx, `
			UPDATE donebot_market_listings
			SET status = 'suspended', last_review_note = $1, updated_at = NOW()
			WHERE id = $2`, note, listingID); err != nil {
			return nil, err
		}
		// Revoke future attribution for suspended listing.
		_, _ = tx.ExecContext(ctx, `UPDATE donebot_market_installs SET status = 'revoked', updated_at = NOW() WHERE listing_id = $1`, listingID)
	case "rerun_ai":
		if latestRelID.Valid && latestRelID.String != "" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE donebot_market_releases SET review_status = 'pending' WHERE id = $1`, latestRelID.String); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE donebot_market_listings
				SET status = CASE WHEN published_release_id IS NOT NULL AND status = 'published' THEN 'published' ELSE 'pending_review' END,
				    updated_at = NOW()
				WHERE id = $1`, listingID); err != nil {
				return nil, err
			}
		}
	case "feature":
		// Only updates featured flag below.
	default:
		return nil, infraerrors.BadRequest("MARKET_DECISION_INVALID", "不支持的审核操作。")
	}

	if featured != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE donebot_market_listings SET featured = $1, updated_at = NOW() WHERE id = $2`, *featured, listingID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if decision == "rerun_ai" {
		s.kickReviewWorker()
	}
	return s.GetListingDetail(ctx, listingID, adminUserID, true)
}

func (s *Service) AdminListReports(ctx context.Context, status string, page, pageSize int) ([]MarketReport, int, error) {
	if err := s.available(); err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	where := "1=1"
	args := make([]any, 0)
	if st := strings.TrimSpace(status); st != "" && st != "all" {
		where = "r.status = $1"
		args = append(args, st)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_market_reports r WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	idx := len(args) + 1
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT r.id, r.listing_id, l.name, r.reporter_user_id, u.email, r.reason_type, r.detail,
		       r.status, r.handle_note, r.handled_at, r.created_at
		FROM donebot_market_reports r
		JOIN donebot_market_listings l ON l.id = r.listing_id
		JOIN users u ON u.id = r.reporter_user_id
		WHERE %s
		ORDER BY CASE WHEN r.status = 'open' THEN 0 ELSE 1 END, r.created_at DESC
		LIMIT $%d OFFSET $%d`, where, idx, idx+1), append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]MarketReport, 0)
	for rows.Next() {
		var rep MarketReport
		if err := rows.Scan(&rep.ID, &rep.ListingID, &rep.ListingName, &rep.ReporterUserID, &rep.ReporterEmail,
			&rep.ReasonType, &rep.Detail, &rep.Status, &rep.HandleNote, &rep.HandledAt, &rep.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, rep)
	}
	return out, total, rows.Err()
}

func (s *Service) AdminHandleReport(ctx context.Context, adminUserID, reportID int64, status, note string) error {
	if err := s.available(); err != nil {
		return err
	}
	if status != "resolved" && status != "dismissed" {
		return infraerrors.BadRequest("MARKET_REPORT_STATUS_INVALID", "无效的处理状态。")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE donebot_market_reports
		SET status = $1, handled_by = $2, handle_note = $3, handled_at = NOW()
		WHERE id = $4`, status, adminUserID, strings.TrimSpace(note), reportID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return infraerrors.NotFound("MARKET_REPORT_NOT_FOUND", "举报记录不存在。")
	}
	return nil
}
