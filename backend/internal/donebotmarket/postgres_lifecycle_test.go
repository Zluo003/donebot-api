package donebotmarket

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func TestPostgresFullReviewLifecycle(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("DONEBOT_MARKET_TEST_DSN"))
	if dsn == "" {
		t.Skip("set DONEBOT_MARKET_TEST_DSN to run PostgreSQL lifecycle tests")
	}
	adminDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open admin db: %v", err)
	}
	defer func() { _ = adminDB.Close() }()

	schema := fmt.Sprintf("donebot_market_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer func() { _, _ = adminDB.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }()

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("postgres", dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer func() { _ = db.Close() }()

	prereq := `
		CREATE TABLE users (
			id BIGSERIAL PRIMARY KEY,
			email VARCHAR(255) NOT NULL DEFAULT '',
			username VARCHAR(120) NOT NULL DEFAULT '',
			balance NUMERIC(20, 6) NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		);
		CREATE TABLE user_avatars (
			user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			url TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE proxies (
			id BIGSERIAL PRIMARY KEY,
			protocol VARCHAR(16) NOT NULL DEFAULT 'http',
			host VARCHAR(255) NOT NULL DEFAULT '',
			port INT NOT NULL DEFAULT 0,
			username VARCHAR(128) NOT NULL DEFAULT '',
			password VARCHAR(128) NOT NULL DEFAULT ''
		);
		CREATE TABLE accounts (
			id BIGSERIAL PRIMARY KEY,
			name VARCHAR(120) NOT NULL DEFAULT '',
			platform VARCHAR(64) NOT NULL DEFAULT '',
			type VARCHAR(32) NOT NULL DEFAULT 'apikey',
			status VARCHAR(32) NOT NULL DEFAULT 'active',
			schedulable BOOLEAN NOT NULL DEFAULT TRUE,
			credentials JSONB NOT NULL DEFAULT '{}'::jsonb,
			extra JSONB NOT NULL DEFAULT '{}'::jsonb,
			proxy_id BIGINT REFERENCES proxies(id),
			priority INT NOT NULL DEFAULT 0,
			deleted_at TIMESTAMPTZ
		);
		CREATE TABLE settings (
			key VARCHAR(128) PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO users (id, email, username, balance) VALUES
			(1, 'creator@example.com', 'creator', 10.0),
			(2, 'installer@example.com', 'installer', 500.0);
		INSERT INTO user_avatars (user_id, url) VALUES (1, 'https://example.com/a.png');
		INSERT INTO accounts (name, platform, type, status, schedulable, credentials) VALUES
			('TestOpenAI', 'openai', 'apikey', 'active', TRUE, '{"api_key":"sk-test","base_url":"https://api.openai.com"}');
	`
	if _, err := db.Exec(prereq); err != nil {
		t.Fatalf("create prereq tables: %v", err)
	}

	for _, mig := range []string{
		"287_donebot_bot_market.sql",
		"288_donebot_market_review_account.sql",
		"289_donebot_market_studio_and_spend.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", mig))
		if err != nil {
			t.Fatalf("read migration %s: %v", mig, err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			t.Fatalf("apply migration %s: %v", mig, err)
		}
	}

	ctx := context.Background()
	svc := New(db, true)

	// 1. Settings & ReviewOptions & Categories
	if _, err := svc.GetSettings(ctx); err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	accID := int64(1)
	freezeZero := 0
	bonus10 := 10.0
	if _, err := svc.UpdateSettings(ctx, UpdateSettingsInput{
		ReviewAccountID:      &accID,
		FreezeDays:           &freezeZero,
		TransferBonusPercent: &bonus10,
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if opts, err := svc.AdminReviewOptions(ctx); err != nil || len(opts.Accounts) != 1 {
		t.Fatalf("AdminReviewOptions: err=%v opts=%+v", err, opts)
	}
	cats, err := svc.ListCategories(ctx, true)
	if err != nil || len(cats) == 0 {
		t.Fatalf("ListCategories: err=%v len=%d", err, len(cats))
	}
	catEnabled := true
	savedCat, err := svc.SaveCategory(ctx, SaveCategoryInput{Slug: "novel-test", Name: "小说测试", Description: "测试分类", SortOrder: 99, Enabled: &catEnabled})
	if err != nil {
		t.Fatalf("SaveCategory: %v", err)
	}
	if err := svc.DeleteCategory(ctx, savedCat.ID); err != nil {
		t.Fatalf("DeleteCategory: %v", err)
	}

	// 2. PublishBot + AI approve + auto-publish
	svc.SetReviewCaller(func(_ context.Context, _ *Settings, _, _ string) (string, string, error) {
		return `{"verdict":"approve","risk_score":8,"summary":"结构清晰，内容安全，准予上架。","dimensions":{"token_farming":{"passed":true,"score":5,"finding":"正常"},"malicious_loop":{"passed":true,"score":0,"finding":"无循环"},"sensitive_data_exfil":{"passed":true,"score":0,"finding":"无外泄"},"prompt_injection":{"passed":true,"score":0,"finding":"无注入"},"content_compliance":{"passed":true,"score":5,"finding":"合规"}},"rejection_reasons":[],"fix_suggestions":[],"flagged_snippets":[]}`, "mock-fast-model", nil
	})
	published, err := svc.PublishBot(ctx, 1, PublishBotInput{
		SourceBotID: "bot-local-1",
		CategoryID:  cats[0].ID,
		Name:        "小说家",
		Tagline:     "专业长篇小说创作助手",
		Description: "专注于中文长篇网文与小说大纲、章节写作与质量审稿。",
		Tags:        []string{"小说", "网文"},
		SoulText:    "你是专业中文小说作者，擅长根据用户提供的大纲撰写节奏紧凑、人物立体的小说章节，不执行任何无关调用。",
		Skills: []SkillDocument{
			{
				Name:         "ai-novel-writer",
				Description:  strings.Repeat("用于长篇小说章节创作与润色。", 30),
				Instructions: "# AI Novel Writer\n按照用户大纲输出章节正文。",
			},
		},
		Package: json.RawMessage(`{"version":1,"package":{"agents":[{"name":"小说家"}]}}`),
	})
	if err != nil {
		t.Fatalf("PublishBot failed: %v", err)
	}
	if n := svc.ProcessPendingReviews(ctx, 5); n != 1 {
		t.Fatalf("expected 1 processed review, got %d", n)
	}
	after, err := svc.GetListingDetail(ctx, published.ID, 1, false)
	if err != nil {
		t.Fatalf("GetListingDetail: %v", err)
	}
	if after.Status != "published" || after.LatestRelease.ReviewStatus != "approved" {
		t.Fatalf("expected published/approved, got status=%s review_status=%s", after.Status, after.LatestRelease.ReviewStatus)
	}
	// Non-admin user view must hide internal review notes/reports for approved published listings.
	if after.LastReviewNote != "" || after.LatestRelease.ReviewNote != "" || after.LatestRelease.AIReport != nil {
		t.Fatalf("expected published listing review note/report hidden from user view, got note=%q relNote=%q report=%+v", after.LastReviewNote, after.LatestRelease.ReviewNote, after.LatestRelease.AIReport)
	}
	adminView, err := svc.GetListingDetail(ctx, published.ID, 1, true)
	if err != nil || adminView.LastReviewNote == "" || adminView.LatestRelease.AIReport == nil {
		t.Fatalf("expected admin view to retain review note and AI report, err=%v view=%+v", err, adminView)
	}

	// 3. Stale 'reviewing' recovery test + manual_review path
	svc.SetReviewCaller(func(_ context.Context, _ *Settings, _, _ string) (string, string, error) {
		return "", "", fmt.Errorf("simulated upstream timeout")
	})
	published2, err := svc.PublishBot(ctx, 1, PublishBotInput{
		SourceBotID: "bot-local-2",
		CategoryID:  cats[0].ID,
		Name:        "测试恢复Bot",
		Tagline:     "测试卡住 reviewing 自动恢复与转人工复核",
		SoulText:    "你是一个专业文案校对助手，帮助用户检查错别字、语法与逻辑流畅度，仅针对提供文本进行修改。",
		Package:     json.RawMessage(`{"version":1,"package":{"agents":[{"name":"测试恢复Bot"}]}}`),
	})
	if err != nil {
		t.Fatalf("PublishBot 2: %v", err)
	}
	// Simulate stuck 'reviewing' row from 5 minutes ago.
	if _, err := db.Exec(`UPDATE donebot_market_releases SET review_status = 'reviewing', created_at = NOW() - INTERVAL '5 minutes' WHERE id = $1`, published2.LatestRelease.ID); err != nil {
		t.Fatalf("simulate stale reviewing: %v", err)
	}
	if _, err := db.Exec(`UPDATE donebot_market_listings SET updated_at = NOW() - INTERVAL '5 minutes' WHERE id = $1`, published2.ID); err != nil {
		t.Fatalf("simulate stale listing: %v", err)
	}
	if n := svc.ProcessPendingReviews(ctx, 5); n != 1 {
		t.Fatalf("expected stale reviewing release to be reclaimed and processed, got %d", n)
	}
	after2, err := svc.GetListingDetail(ctx, published2.ID, 1, false)
	if err != nil {
		t.Fatalf("GetListingDetail 2: %v", err)
	}
	if after2.Status != "manual_review" || after2.LatestRelease.ReviewStatus != "manual_review" {
		t.Fatalf("expected manual_review after timeout, got status=%s review_status=%s", after2.Status, after2.LatestRelease.ReviewStatus)
	}

	// 4. Browse / MyListings / Visibility / Install / Report
	for _, sortKey := range []string{"installs", "newest", "revenue"} {
		if items, total, err := svc.ListMarketplace(ctx, MarketplaceFilter{Sort: sortKey, Query: "小说", Page: 1, PageSize: 20}); err != nil || total != 1 || len(items) != 1 {
			t.Fatalf("ListMarketplace(sort=%s): err=%v total=%d len=%d", sortKey, err, total, len(items))
		}
	}
	if mine, err := svc.ListMyListings(ctx, 1); err != nil || len(mine) != 2 {
		t.Fatalf("ListMyListings: err=%v len=%d", err, len(mine))
	}
	// Approved/published listing cannot be unlisted by author.
	if err := svc.SetAuthorListingVisibility(ctx, 1, published.ID, false); err == nil {
		t.Fatalf("expected author unlist of approved listing to be forbidden")
	}

	// Self-install by author must succeed for local use, mark SelfInstall=true, not increment install_count, and accrue zero rebates.
	selfInst, err := svc.InstallListing(ctx, 1, published.ID, "inst-author-self")
	if err != nil || !selfInst.SelfInstall {
		t.Fatalf("self InstallListing: err=%v selfInst=%+v", err, selfInst)
	}
	if err := svc.AccrueLLMUsage(ctx, 1, "req-self-llm-1", 5000.0, selfInst.InstallToken); err != nil {
		t.Fatalf("self AccrueLLMUsage: %v", err)
	}
	svc.RecordMediaAttribution(ctx, "rcpt-self-img-1", 1, "image", selfInst.InstallToken)
	if err := svc.AccrueMediaSettlement(ctx, "rcpt-self-img-1", 1, 200.0, false); err != nil {
		t.Fatalf("self AccrueMediaSettlement: %v", err)
	}
	if w, err := svc.GetCreatorWallet(ctx, 1); err != nil || w.TotalEarned != 0 || w.FrozenAmount != 0 || w.AvailableAmount != 0 {
		t.Fatalf("expected zero creator reward from self-install, err=%v wallet=%+v", err, w)
	}
	if detailAfterSelf, err := svc.GetListingDetail(ctx, published.ID, 1, false); err != nil || detailAfterSelf.InstallCount != 0 {
		t.Fatalf("expected self-install not to increment install_count, err=%v count=%d", err, detailAfterSelf.InstallCount)
	}

	inst, err := svc.InstallListing(ctx, 2, published.ID, "inst-device-1")
	if err != nil || inst.InstallToken == "" {
		t.Fatalf("InstallListing: err=%v inst=%+v", err, inst)
	}
	if err := svc.ReportListing(ctx, 2, published.ID, ReportListingInput{ReasonType: "token_abuse", Detail: "测试举报别名"}); err != nil {
		t.Fatalf("ReportListing: %v", err)
	}

	// 5. Billing: LLM + Media attribution + Thaw + Wallet + Ledger + Transfer + Withdraw + Admin process
	if err := svc.AccrueLLMUsage(ctx, 2, "req-llm-1", 4000.0, inst.InstallToken); err != nil {
		t.Fatalf("AccrueLLMUsage: %v", err)
	}
	svc.RecordMediaAttribution(ctx, "rcpt-img-1", 2, "image", inst.InstallToken)
	if err := svc.AccrueMediaSettlement(ctx, "rcpt-img-1", 2, 100.0, false); err != nil {
		t.Fatalf("AccrueMediaSettlement: %v", err)
	}
	if _, err := svc.ThawAllMaturedRewards(ctx); err != nil {
		t.Fatalf("ThawAllMaturedRewards: %v", err)
	}
	wallet, err := svc.GetCreatorWallet(ctx, 1)
	if err != nil || wallet.AvailableAmount <= 0 {
		t.Fatalf("GetCreatorWallet: err=%v wallet=%+v", err, wallet)
	}
	if entries, total, err := svc.ListCreatorLedger(ctx, 1, 1, 20); err != nil || total < 2 || len(entries) < 2 {
		t.Fatalf("ListCreatorLedger: err=%v total=%d len=%d", err, total, len(entries))
	}
	if tr, err := svc.TransferToBalance(ctx, 1, 50.0); err != nil || tr.BonusAmount != 5.0 || tr.CreditAmount != 55.0 {
		t.Fatalf("TransferToBalance: err=%v tr=%+v", err, tr)
	}
	wd, err := svc.RequestWithdrawal(ctx, 1, 100.0, "creator@alipay.com", "测试创作者")
	if err != nil || wd.Status != "pending" {
		t.Fatalf("RequestWithdrawal: err=%v wd=%+v", err, wd)
	}
	if wds, total, err := svc.ListCreatorWithdrawals(ctx, 1, 1, 20); err != nil || total != 1 || len(wds) != 1 {
		t.Fatalf("ListCreatorWithdrawals: err=%v total=%d", err, total)
	}
	if wds, total, err := svc.AdminListWithdrawals(ctx, "pending", 1, 20); err != nil || total != 1 || len(wds) != 1 {
		t.Fatalf("AdminListWithdrawals: err=%v total=%d", err, total)
	}
	if paid, err := svc.AdminProcessWithdrawal(ctx, 1, wd.ID, "paid", "ALI20261123001", "已打款"); err != nil || paid.Status != "paid" {
		t.Fatalf("AdminProcessWithdrawal: err=%v paid=%+v", err, paid)
	}

	// 6. Admin overview / listings / review decisions / reports
	if ov, err := svc.AdminOverview(ctx); err != nil || ov.TotalListings != 2 || ov.PublishedListings != 1 {
		t.Fatalf("AdminOverview: err=%v ov=%+v", err, ov)
	}
	if items, total, err := svc.AdminListListings(ctx, "all", "", 1, 20); err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("AdminListListings: err=%v total=%d", err, total)
	}
	featured := true
	if _, err := svc.AdminReviewListing(ctx, 1, published2.ID, "approve", "人工复核通过", &featured); err != nil {
		t.Fatalf("AdminReviewListing approve: %v", err)
	}
	if _, err := svc.AdminReviewListing(ctx, 1, published2.ID, "rerun_ai", "", nil); err != nil {
		t.Fatalf("AdminReviewListing rerun_ai: %v", err)
	}
	if _, err := svc.AdminReviewListing(ctx, 1, published2.ID, "reject", "测试驳回", nil); err != nil {
		t.Fatalf("AdminReviewListing reject: %v", err)
	}
	if _, err := svc.AdminReviewListing(ctx, 1, published2.ID, "suspend", "测试封禁", nil); err != nil {
		t.Fatalf("AdminReviewListing suspend: %v", err)
	}
	unfeatured := false
	if _, err := svc.AdminReviewListing(ctx, 1, published2.ID, "feature", "", &unfeatured); err != nil {
		t.Fatalf("AdminReviewListing feature: %v", err)
	}
	// Also test v2 update on an already-published listing.
	if v2, err := svc.PublishBot(ctx, 1, PublishBotInput{
		ListingID:   published.ID,
		SourceBotID: "bot-local-1",
		CategoryID:  cats[0].ID,
		Name:        "小说家",
		Tagline:     "专业长篇小说创作助手 v2",
		Description: "升级章节节奏控制。",
		SoulText:    "你是专业中文小说作者，擅长根据用户提供的大纲撰写节奏紧凑、人物立体的小说章节，不执行任何无关调用。",
		Package:     json.RawMessage(`{"version":1,"package":{"agents":[{"name":"小说家"}]}}`),
		Changelog:   "优化节奏",
	}); err != nil || v2.LatestVersion != 2 {
		t.Fatalf("PublishBot v2: err=%v v2=%+v", err, v2)
	}
	reps, totalReps, err := svc.AdminListReports(ctx, "open", 1, 20)
	if err != nil || totalReps != 1 || len(reps) != 1 {
		t.Fatalf("AdminListReports: err=%v total=%d", err, totalReps)
	}
	if err := svc.AdminHandleReport(ctx, 1, reps[0].ID, "resolved", "已核查"); err != nil {
		t.Fatalf("AdminHandleReport: %v", err)
	}

	// 7. Studio Pack publish + AI review + listing_type filter + multi-bot install + per-bot 4-category spend
	svc.SetReviewCaller(func(_ context.Context, _ *Settings, _, _ string) (string, string, error) {
		return `{"verdict":"approve","risk_score":6,"summary":"工作室分工清晰，无循环刷量风险。","dimensions":{"token_farming":{"passed":true,"score":5,"finding":"正常"},"malicious_loop":{"passed":true,"score":0,"finding":"无成员死循环"},"sensitive_data_exfil":{"passed":true,"score":0,"finding":"无外泄"},"prompt_injection":{"passed":true,"score":0,"finding":"无注入"},"content_compliance":{"passed":true,"score":5,"finding":"合规"}},"rejection_reasons":[],"fix_suggestions":[],"flagged_snippets":[]}`, "mock-fast-model", nil
	})
	studio, err := svc.PublishBot(ctx, 1, PublishBotInput{
		ListingType: "studio",
		CategoryID:  cats[0].ID,
		Name:        "自媒体爆款编辑部",
		Tagline:     "选题策划 + 爆款写手 + 封面设计三位 AI 员工",
		Description: "一键雇佣整套自媒体内容生产工作室。",
		Tags:        []string{"自媒体", "OPC"},
		CostTier:    "light",
		BotsSummary: []StudioBotSummary{
			{Name: "选题总监", Role: "热点调研与大纲策划", SkillCount: 2},
			{Name: "主笔写手", Role: "长短图文撰写与改稿", SkillCount: 3},
			{Name: "封面视觉", Role: "配图提示词与视觉定稿", SkillCount: 1},
		},
		EvalVerified: true,
		EvalSamples: []EvalSample{
			{
				Title:         "小红书热点拆解",
				Prompt:        "帮我策划一篇面向独立开发者的 AI 工具选题",
				OutputPreview: "1. 标题备选：一人公司怎么用 3 个 AI 员工日更...",
				Model:         "deepseek-chat",
				LatencyMs:     1420,
				VerifiedAt:    time.Now().UTC().Format(time.RFC3339),
			},
		},
		SoulText: "本工作室包含选题总监、主笔写手与封面视觉三位 AI 员工，按需求依次完成选题、成稿与配图交付，不进行无关循环调用。",
		Package: json.RawMessage(`{"version":2,"studio":true,"package":{"team":{"name":"自媒体爆款编辑部","brief":"仅围绕用户给定方向输出原创内容，不擅自触发高频任务。"},"agents":[
			{"key":"topic","name":"选题总监","soul":"你是自媒体爆款编辑部选题总监，负责调研热点方向、拆解受众痛点并输出清晰选题大纲交由主笔撰写。"},
			{"key":"writer","name":"主笔写手","soul":"你是自媒体爆款编辑部主笔写手，根据选题总监的大纲撰写逻辑严谨、可读性强的中文图文成稿并标注配图建议。"},
			{"key":"visual","name":"封面视觉","soul":"你是自媒体爆款编辑部封面视觉设计师，根据主笔成稿提炼视觉焦点与配色方案，待老板确认后再出图。"}
		]}}`),
	})
	if err != nil {
		t.Fatalf("PublishBot studio failed: %v", err)
	}
	if studio.ListingType != "studio" || studio.BotCount != 3 || !studio.EvalVerified || studio.CostTier != "light" {
		t.Fatalf("unexpected studio fields: %+v", studio)
	}
	if n := svc.ProcessPendingReviews(ctx, 5); n < 1 {
		t.Fatalf("expected studio review processed, got %d", n)
	}
	studioItems, studioTotal, err := svc.ListMarketplace(ctx, MarketplaceFilter{ListingType: "studio", Page: 1, PageSize: 20})
	if err != nil || studioTotal != 1 || len(studioItems) != 1 || studioItems[0].BotCount != 3 || !studioItems[0].EvalVerified {
		t.Fatalf("ListMarketplace(studio): err=%v total=%d items=%+v", err, studioTotal, studioItems)
	}
	botOnlyItems, botOnlyTotal, err := svc.ListMarketplace(ctx, MarketplaceFilter{ListingType: "bot", Page: 1, PageSize: 20})
	if err != nil || botOnlyTotal != 1 || len(botOnlyItems) != 1 || botOnlyItems[0].ListingType != "bot" {
		t.Fatalf("ListMarketplace(bot): err=%v total=%d items=%+v", err, botOnlyTotal, botOnlyItems)
	}
	studioInstall, err := svc.InstallListing(ctx, 2, studio.ID, "inst-device-studio")
	if err != nil || studioInstall.ListingType != "studio" || studioInstall.BotCount != 3 || len(studioInstall.BotsSummary) != 3 {
		t.Fatalf("InstallListing studio: err=%v inst=%+v", err, studioInstall)
	}

	// Per-bot actual spend tracking via BillingHookAdapter (studio bots share installToken + have individual bot_ids)
	adapter := NewBillingHookAdapter(svc)
	adapter.OnLLMSettled(ctx, 2, "studio-llm-1", 12.5, studioInstall.InstallToken, "bot-editor-1")
	adapter.OnLLMSettled(ctx, 2, "studio-llm-1", 12.5, studioInstall.InstallToken, "bot-editor-1") // idempotent retry
	adapter.OnLLMSettled(ctx, 2, "studio-llm-2", 4.25, "", "bot-private-self")                      // non-market personal bot!
	adapter.OnMediaAdmitted(ctx, "rcpt-studio-img-1", 2, "image", studioInstall.InstallToken, "bot-cover-3")
	adapter.OnMediaSettled(ctx, "rcpt-studio-img-1", 2, 3.0, false)
	adapter.OnMediaAdmitted(ctx, "rcpt-studio-vid-failed", 2, "video", studioInstall.InstallToken, "bot-cover-3")
	adapter.OnMediaSettled(ctx, "rcpt-studio-vid-failed", 2, 10.0, true) // failed must not be recorded

	spend, err := svc.GetUserBotSpendSummary(ctx, 2, 30)
	if err != nil {
		t.Fatalf("GetUserBotSpendSummary: %v", err)
	}
	if spend.TotalCost != 19.75 || spend.LLMCost != 16.75 || spend.ImageCost != 3.0 || len(spend.Bots) != 3 {
		t.Fatalf("unexpected spend summary: %+v", spend)
	}
	if spend.Bots[0].BotID != "bot-editor-1" || spend.Bots[0].LLMCost != 12.5 {
		t.Fatalf("expected top bot bot-editor-1 with 12.5, got %+v", spend.Bots[0])
	}
}
