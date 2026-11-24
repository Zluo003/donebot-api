package donebotmarket

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	MaxPackageBytes       = 4 << 20 // 4 MiB (matches DoneBot PACKAGE_MAX_BYTES)
	MaxTagsPerListing     = 6
	MaxSamplePrompts      = 3
	MaxSkillsPerBot       = 30
	MaxSkillsPerStudio    = 90
	MaxBotsPerStudio      = 8
	MaxEvalSamples        = 5
	MinSoulRunes          = 20
	MaxSoulRunes          = 100000
	MaxSkillDescRunes     = 2000      // DoneBot SKILL.md DESCRIPTION_MAX=1024 with safety headroom
	MaxSkillInstrRunes    = 256 * 1024 // Matches DoneBot SKILL_FILE_MAX_BYTES=256 KiB
	MinTransferAmount     = 1.0
	InstallTokenHeader    = "X-DoneBot-Market-Install"
	BotIDHeader           = "X-DoneBot-Bot-Id"
	DefaultAttrDays       = 180
	DefaultFreezeDays     = 7
	DefaultMinWithdraw    = 100.0
	DefaultRebateLLM      = 5.0
	DefaultRebateImage    = 3.0
	DefaultRebateVideo    = 2.0
	DefaultRebateMusic    = 2.0
)

type Settings struct {
	Enabled                bool      `json:"enabled"`
	AutoPublishOnAIApprove bool      `json:"auto_publish_on_ai_approve"`
	ReviewAccountID        *int64    `json:"review_account_id,omitempty"`
	ReviewModel            string    `json:"review_model"`
	ReviewAPIBase          string    `json:"review_api_base"`
	ReviewAPIKeyConfigured bool      `json:"review_api_key_configured"`
	AttributionDays        int       `json:"attribution_days"`
	FreezeDays             int       `json:"freeze_days"`
	MinWithdrawAmount      float64   `json:"min_withdraw_amount"`
	TransferBonusPercent   float64   `json:"transfer_bonus_percent"`
	RebateLLMPercent       float64   `json:"rebate_llm_percent"`
	RebateImagePercent     float64   `json:"rebate_image_percent"`
	RebateVideoPercent     float64   `json:"rebate_video_percent"`
	RebateMusicPercent     float64   `json:"rebate_music_percent"`
	UpdatedAt              time.Time `json:"updated_at"`

	reviewAPIKey  string
	signingSecret string
}

type UpdateSettingsInput struct {
	Enabled                *bool    `json:"enabled"`
	AutoPublishOnAIApprove *bool    `json:"auto_publish_on_ai_approve"`
	ReviewAccountID        *int64   `json:"review_account_id"`
	ReviewModel            *string  `json:"review_model"`
	ReviewAPIBase          *string  `json:"review_api_base"`
	ReviewAPIKey           *string  `json:"review_api_key"`
	ClearReviewAPIKey      bool     `json:"clear_review_api_key"`
	AttributionDays        *int     `json:"attribution_days"`
	FreezeDays             *int     `json:"freeze_days"`
	MinWithdrawAmount      *float64 `json:"min_withdraw_amount"`
	TransferBonusPercent   *float64 `json:"transfer_bonus_percent"`
	RebateLLMPercent       *float64 `json:"rebate_llm_percent"`
	RebateImagePercent     *float64 `json:"rebate_image_percent"`
	RebateVideoPercent     *float64 `json:"rebate_video_percent"`
	RebateMusicPercent     *float64 `json:"rebate_music_percent"`
}

type ReviewAccountOption struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Platform     string   `json:"platform"`
	Type         string   `json:"type"`
	Status       string   `json:"status"`
	Schedulable  bool     `json:"schedulable"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model,omitempty"`
}

type ReviewOptions struct {
	Accounts []ReviewAccountOption `json:"accounts"`
	Models   []string              `json:"models"`
}

type ReviewTestInput struct {
	ReviewAccountID *int64 `json:"review_account_id"`
	ReviewModel     string `json:"review_model"`
	ReviewAPIBase   string `json:"review_api_base"`
	ReviewAPIKey    string `json:"review_api_key"`
}

type ReviewTestResult struct {
	OK         bool   `json:"ok"`
	Model      string `json:"model"`
	Account    string `json:"account,omitempty"`
	LatencyMs  int64  `json:"latency_ms"`
	Verdict    string `json:"verdict,omitempty"`
	RiskScore  int    `json:"risk_score,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Category struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	SortOrder   int       `json:"sort_order"`
	Enabled     bool      `json:"enabled"`
	ListingCnt  int       `json:"listing_count,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SaveCategoryInput struct {
	ID          int64  `json:"id,omitempty"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

type SkillDocument struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Instructions string `json:"instructions"`
}

type StudioBotSummary struct {
	Name       string `json:"name"`
	Role       string `json:"role,omitempty"`
	Avatar     string `json:"avatar,omitempty"`
	Color      string `json:"color,omitempty"`
	SkillCount int    `json:"skill_count,omitempty"`
}

type EvalSample struct {
	Title         string `json:"title,omitempty"`
	Prompt        string `json:"prompt"`
	OutputPreview string `json:"output_preview"`
	Model         string `json:"model,omitempty"`
	LatencyMs     int64  `json:"latency_ms,omitempty"`
	VerifiedAt    string `json:"verified_at,omitempty"`
}

type BotSpendRow struct {
	BotID      string    `json:"bot_id"`
	TotalCost  float64   `json:"total_cost"`
	LLMCost    float64   `json:"llm_cost"`
	ImageCost  float64   `json:"image_cost"`
	VideoCost  float64   `json:"video_cost"`
	MusicCost  float64   `json:"music_cost"`
	EventCount int       `json:"event_count"`
	LastUsedAt time.Time `json:"last_used_at"`
}

type BotSpendSummary struct {
	Days      int           `json:"days"`
	TotalCost float64       `json:"total_cost"`
	LLMCost   float64       `json:"llm_cost"`
	ImageCost float64       `json:"image_cost"`
	VideoCost float64       `json:"video_cost"`
	MusicCost float64       `json:"music_cost"`
	Bots      []BotSpendRow `json:"bots"`
}

type PublishBotInput struct {
	ListingID     string             `json:"listing_id,omitempty"`
	ListingType   string             `json:"listing_type,omitempty"`
	SourceBotID   string             `json:"source_bot_id,omitempty"`
	CategoryID    int64              `json:"category_id"`
	Name          string             `json:"name"`
	Tagline       string             `json:"tagline"`
	Description   string             `json:"description"`
	Tags          []string           `json:"tags"`
	SamplePrompts []string           `json:"sample_prompts"`
	Appearance    json.RawMessage    `json:"appearance,omitempty"`
	BotsSummary   []StudioBotSummary `json:"bots_summary,omitempty"`
	EvalVerified  bool               `json:"eval_verified,omitempty"`
	EvalSamples   []EvalSample       `json:"eval_samples,omitempty"`
	CostTier      string             `json:"cost_tier,omitempty"`
	SoulText      string             `json:"soul_text"`
	Skills        []SkillDocument    `json:"skills"`
	Package       json.RawMessage    `json:"package"`
	Changelog     string             `json:"changelog,omitempty"`
}

type ListingSummary struct {
	ID                  string             `json:"id"`
	ListingType         string             `json:"listing_type"`
	BotCount            int                `json:"bot_count"`
	BotsSummary         []StudioBotSummary `json:"bots_summary"`
	EvalVerified        bool               `json:"eval_verified"`
	EvalSamples         []EvalSample       `json:"eval_samples"`
	CostTier            string             `json:"cost_tier"`
	AuthorUserID        int64              `json:"author_user_id"`
	AuthorName          string             `json:"author_name"`
	AuthorAvatarURL     string             `json:"author_avatar_url,omitempty"`
	CategoryID          int64              `json:"category_id"`
	CategoryName        string             `json:"category_name"`
	CategorySlug        string             `json:"category_slug"`
	SourceBotID         string             `json:"source_bot_id,omitempty"`
	Name                string             `json:"name"`
	Tagline             string             `json:"tagline"`
	Description         string             `json:"description"`
	Tags                []string           `json:"tags"`
	SamplePrompts       []string           `json:"sample_prompts"`
	Appearance          json.RawMessage    `json:"appearance,omitempty"`
	HasSoul             bool               `json:"has_soul"`
	SkillCount          int                `json:"skill_count"`
	SkillNames          []string           `json:"skill_names"`
	LatestReleaseID     string             `json:"latest_release_id,omitempty"`
	PublishedReleaseID  string             `json:"published_release_id,omitempty"`
	LatestVersion       int                `json:"latest_version"`
	PublishedVersion    int                `json:"published_version"`
	Status              string             `json:"status"`
	Featured            bool               `json:"featured"`
	InstallCount        int                `json:"install_count"`
	TotalAttributedCost float64            `json:"total_attributed_cost,omitempty"`
	TotalCreatorReward  float64            `json:"total_creator_reward,omitempty"`
	ReportCount         int                `json:"report_count,omitempty"`
	LastReviewNote      string             `json:"last_review_note,omitempty"`
	PublishedAt         *time.Time         `json:"published_at,omitempty"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

type ReleaseDetail struct {
	ID            string          `json:"id"`
	ListingID     string          `json:"listing_id"`
	Version       int             `json:"version"`
	Changelog     string          `json:"changelog"`
	SoulText      string          `json:"soul_text"`
	Skills        []SkillDocument `json:"skills"`
	Package       json.RawMessage `json:"package,omitempty"`
	PackageSHA256 string          `json:"package_sha256"`
	PackageBytes  int             `json:"package_bytes"`
	ReviewStatus  string          `json:"review_status"`
	AIVerdict     string          `json:"ai_verdict,omitempty"`
	AIRiskScore   int             `json:"ai_risk_score"`
	AIReport      *AIReviewReport `json:"ai_report,omitempty"`
	ReviewNote    string          `json:"review_note,omitempty"`
	ReviewedAt    *time.Time      `json:"reviewed_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type ListingDetail struct {
	ListingSummary
	SoulPreview   string          `json:"soul_preview"`
	Skills        []SkillDocument `json:"skills"`
	LatestRelease *ReleaseDetail  `json:"latest_release,omitempty"`
}

type InstallResult struct {
	InstallID     string             `json:"install_id"`
	ListingID     string             `json:"listing_id"`
	ListingType   string             `json:"listing_type"`
	BotCount      int                `json:"bot_count"`
	BotsSummary   []StudioBotSummary `json:"bots_summary"`
	ReleaseID     string             `json:"release_id"`
	Version       int                `json:"version"`
	Name          string             `json:"name"`
	InstallToken  string             `json:"install_token"`
	SelfInstall   bool               `json:"self_install,omitempty"`
	ExpiresAt     time.Time          `json:"expires_at"`
	Package       json.RawMessage    `json:"package"`
	Skills        []SkillDocument    `json:"skills"`
	SoulText      string             `json:"soul_text"`
	SamplePrompts []string           `json:"sample_prompts"`
}

type AIReviewDimension struct {
	Passed  bool   `json:"passed"`
	Score   int    `json:"score"`
	Finding string `json:"finding"`
}

type AIReviewReport struct {
	Verdict          string                       `json:"verdict"`
	RiskScore        int                          `json:"risk_score"`
	Summary          string                       `json:"summary"`
	Dimensions       map[string]AIReviewDimension `json:"dimensions,omitempty"`
	RejectionReasons []string                     `json:"rejection_reasons,omitempty"`
	FixSuggestions   []string                     `json:"fix_suggestions,omitempty"`
	FlaggedSnippets  []string                     `json:"flagged_snippets,omitempty"`
	Model            string                       `json:"model,omitempty"`
	CheckedAt        string                       `json:"checked_at,omitempty"`
}

type ReportListingInput struct {
	ReasonType string `json:"reason_type"`
	Detail     string `json:"detail"`
}

type MarketReport struct {
	ID             int64      `json:"id"`
	ListingID      string     `json:"listing_id"`
	ListingName    string     `json:"listing_name"`
	ReporterUserID int64      `json:"reporter_user_id"`
	ReporterEmail  string     `json:"reporter_email,omitempty"`
	ReasonType     string     `json:"reason_type"`
	Detail         string     `json:"detail"`
	Status         string     `json:"status"`
	HandleNote     string     `json:"handle_note,omitempty"`
	HandledAt      *time.Time `json:"handled_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type CreatorWallet struct {
	UserID               int64     `json:"user_id"`
	FrozenAmount         float64   `json:"frozen_amount"`
	AvailableAmount      float64   `json:"available_amount"`
	TotalEarned          float64   `json:"total_earned"`
	TotalTransferred     float64   `json:"total_transferred"`
	TotalWithdrawn       float64   `json:"total_withdrawn"`
	MinWithdrawAmount    float64   `json:"min_withdraw_amount"`
	FreezeDays           int       `json:"freeze_days"`
	TransferBonusPercent float64   `json:"transfer_bonus_percent"`
	RebateLLMPercent     float64   `json:"rebate_llm_percent"`
	RebateImagePercent   float64   `json:"rebate_image_percent"`
	RebateVideoPercent   float64   `json:"rebate_video_percent"`
	RebateMusicPercent   float64   `json:"rebate_music_percent"`
	PendingWithdrawCount int       `json:"pending_withdraw_count"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type RewardLedgerEntry struct {
	ID           int64      `json:"id"`
	ListingID    string     `json:"listing_id,omitempty"`
	ListingName  string     `json:"listing_name,omitempty"`
	EntryType    string     `json:"entry_type"`
	Category     string     `json:"category"`
	BaseCost     float64    `json:"base_cost"`
	RatePercent  float64    `json:"rate_percent"`
	RewardAmount float64    `json:"reward_amount"`
	BonusAmount  float64    `json:"bonus_amount"`
	Status       string     `json:"status"`
	AvailableAt  *time.Time `json:"available_at,omitempty"`
	Note         string     `json:"note,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type WithdrawalRecord struct {
	ID             int64      `json:"id"`
	CreatorUserID  int64      `json:"creator_user_id"`
	CreatorEmail   string     `json:"creator_email,omitempty"`
	CreatorName    string     `json:"creator_name,omitempty"`
	Amount         float64    `json:"amount"`
	AlipayAccount  string     `json:"alipay_account"`
	AlipayRealName string     `json:"alipay_real_name"`
	Status         string     `json:"status"`
	PayoutTradeNo  string     `json:"payout_trade_no,omitempty"`
	AdminNote      string     `json:"admin_note,omitempty"`
	ReviewedAt     *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type TransferToBalanceResult struct {
	Amount       float64 `json:"amount"`
	BonusAmount  float64 `json:"bonus_amount"`
	CreditAmount float64 `json:"credit_amount"`
	NewBalance   float64 `json:"new_balance"`
	Available    float64 `json:"available_amount"`
}

type MarketplaceFilter struct {
	CategoryID  int64
	ListingType string
	Query       string
	Sort        string
	Page        int
	PageSize    int
}

type AdminOverview struct {
	TotalListings       int     `json:"total_listings"`
	PublishedListings   int     `json:"published_listings"`
	PendingReviewCount  int     `json:"pending_review_count"`
	OpenReportCount     int     `json:"open_report_count"`
	PendingWithdrawals  int     `json:"pending_withdrawals"`
	TotalInstalls       int     `json:"total_installs"`
	TotalAttributedCost float64 `json:"total_attributed_cost"`
	TotalCreatorReward  float64 `json:"total_creator_reward"`
	TotalWithdrawn      float64 `json:"total_withdrawn"`
}

func validUUID(raw string) bool {
	v, err := uuid.Parse(strings.TrimSpace(raw))
	return err == nil && v.Version() == 4 && v.String() == strings.TrimSpace(raw)
}

func cleanLine(raw string, maxRunes int, code, msg string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" || !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxRunes || strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return "", infraerrors.BadRequest(code, msg)
	}
	return v, nil
}

func cleanMultiLine(raw string, maxRunes int, required bool, code, msg string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		if required {
			return "", infraerrors.BadRequest(code, msg)
		}
		return "", nil
	}
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxRunes || strings.IndexFunc(v, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	}) >= 0 {
		return "", infraerrors.BadRequest(code, msg)
	}
	return v, nil
}

func normalizeReportReason(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "token_farming", "token_abuse":
		return "token_farming"
	case "malicious_loop":
		return "malicious_loop"
	case "sensitive_file", "data_exfiltration":
		return "sensitive_file"
	case "prompt_injection":
		return "prompt_injection"
	case "illegal_content":
		return "illegal_content"
	case "broken", "spam_low_quality":
		return "broken"
	case "other":
		return "other"
	default:
		return ""
	}
}
