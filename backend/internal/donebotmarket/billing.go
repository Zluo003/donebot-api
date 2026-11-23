package donebotmarket

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

func round6(v float64) float64 {
	return math.Round(v*1_000_000) / 1_000_000
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func (s *Service) rebatePercentForCategory(settings *Settings, category string) float64 {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "llm":
		return settings.RebateLLMPercent
	case "image":
		return settings.RebateImagePercent
	case "video":
		return settings.RebateVideoPercent
	case "music", "audio":
		return settings.RebateMusicPercent
	default:
		return 0
	}
}

func normalizeMediaCategory(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "image", "images":
		return "image"
	case "video", "videos":
		return "video"
	case "music", "audio":
		return "music"
	default:
		return ""
	}
}

// RecordMediaAttribution persists the verified install attribution for an async media receipt
// so terminal settlement in background workers can credit the creator accurately.
func (s *Service) RecordMediaAttribution(ctx context.Context, receiptID string, installerUserID int64, category, installToken string) {
	if !s.Enabled() || strings.TrimSpace(receiptID) == "" || installerUserID <= 0 || strings.TrimSpace(installToken) == "" {
		return
	}
	cat := normalizeMediaCategory(category)
	if cat == "" {
		return
	}
	settings, err := s.GetSettings(ctx)
	if err != nil || !settings.Enabled {
		return
	}
	claims, err := VerifyInstallToken(settings.signingSecret, installToken, time.Now().UTC())
	if err != nil || claims.InstallerUserID != installerUserID || claims.AuthorUserID == installerUserID {
		return
	}
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO donebot_market_media_attributions (
			receipt_id, install_id, listing_id, author_user_id, installer_user_id, category
		)
		SELECT $1, i.id, i.listing_id, i.author_user_id, i.installer_user_id, $2
		FROM donebot_market_installs i
		JOIN donebot_market_listings l ON l.id = i.listing_id
		WHERE i.id = $3 AND i.installer_user_id = $4 AND i.author_user_id <> $4
		  AND i.status = 'active' AND i.expires_at > NOW()
		  AND l.status = 'published'
		ON CONFLICT (receipt_id) DO NOTHING`,
		strings.TrimSpace(receiptID), cat, claims.InstallID, installerUserID)
}

// AccrueLLMUsage credits the bot author for a settled paid LLM usage event (idempotent by usageKey).
func (s *Service) AccrueLLMUsage(ctx context.Context, installerUserID int64, usageKey string, actualCost float64, installToken string) error {
	if !s.Enabled() || installerUserID <= 0 || strings.TrimSpace(usageKey) == "" || actualCost <= 0 || strings.TrimSpace(installToken) == "" {
		return nil
	}
	settings, err := s.GetSettings(ctx)
	if err != nil || !settings.Enabled {
		return nil
	}
	rate := s.rebatePercentForCategory(settings, "llm")
	if rate <= 0 {
		return nil
	}
	claims, err := VerifyInstallToken(settings.signingSecret, installToken, time.Now().UTC())
	if err != nil || claims.InstallerUserID != installerUserID || claims.AuthorUserID == installerUserID {
		return nil
	}
	return s.accrueVerifiedReward(ctx, settings, claims.InstallID, claims.ListingID, claims.AuthorUserID, installerUserID, "llm", "llm:"+strings.TrimSpace(usageKey), actualCost, rate)
}

// AccrueMediaSettlement credits the bot author once an async image/video/music task settles cleanly (non-failure).
func (s *Service) AccrueMediaSettlement(ctx context.Context, receiptID string, installerUserID int64, actualCost float64, failed bool) error {
	if !s.Enabled() || failed || strings.TrimSpace(receiptID) == "" || installerUserID <= 0 || actualCost <= 0 {
		return nil
	}
	settings, err := s.GetSettings(ctx)
	if err != nil || !settings.Enabled {
		return nil
	}
	var installID, listingID, category string
	var authorUserID, storedInstaller int64
	err = s.db.QueryRowContext(ctx, `
		SELECT install_id::text, listing_id::text, author_user_id, installer_user_id, category
		FROM donebot_market_media_attributions
		WHERE receipt_id = $1`, strings.TrimSpace(receiptID)).Scan(&installID, &listingID, &authorUserID, &storedInstaller, &category)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if storedInstaller != installerUserID || authorUserID == installerUserID {
		return nil
	}
	rate := s.rebatePercentForCategory(settings, category)
	if rate <= 0 {
		return nil
	}
	return s.accrueVerifiedReward(ctx, settings, installID, listingID, authorUserID, installerUserID, category, "media:"+strings.TrimSpace(receiptID), actualCost, rate)
}

func (s *Service) accrueVerifiedReward(
	ctx context.Context,
	settings *Settings,
	installID, listingID string,
	authorUserID, installerUserID int64,
	category, sourceKey string,
	baseCost, ratePercent float64,
) error {
	if authorUserID <= 0 || installerUserID <= 0 || authorUserID == installerUserID {
		return nil
	}
	reward := round6(baseCost * ratePercent / 100.0)
	if reward <= 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Verify active, unexpired install and published listing under DB authority.
	var valid int
	err = tx.QueryRowContext(ctx, `
		SELECT 1
		FROM donebot_market_installs i
		JOIN donebot_market_listings l ON l.id = i.listing_id
		WHERE i.id = $1 AND i.listing_id = $2 AND i.author_user_id = $3 AND i.installer_user_id = $4
		  AND i.status = 'active' AND i.expires_at > NOW()
		  AND l.status = 'published'`,
		installID, listingID, authorUserID, installerUserID).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	status := "frozen"
	availableAt := time.Now().UTC().Add(time.Duration(settings.FreezeDays) * 24 * time.Hour)
	if settings.FreezeDays == 0 {
		status = "available"
		availableAt = time.Now().UTC()
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO donebot_creator_reward_ledger (
			creator_user_id, listing_id, install_id, installer_user_id,
			entry_type, category, source_key, base_cost, rate_percent,
			reward_amount, status, available_at, note
		) VALUES ($1,$2,$3,$4,'accrue',$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (source_key) DO NOTHING`,
		authorUserID, listingID, installID, installerUserID,
		category, sourceKey, round6(baseCost), ratePercent,
		reward, status, availableAt, fmt.Sprintf("%s 消费返现 (%.2f%%)", strings.ToUpper(category), ratePercent))
	if err != nil {
		return err
	}
	inserted, _ := res.RowsAffected()
	if inserted == 0 {
		return nil
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO donebot_creator_wallets (user_id, frozen_amount, available_amount, total_earned)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET
			frozen_amount = donebot_creator_wallets.frozen_amount + EXCLUDED.frozen_amount,
			available_amount = donebot_creator_wallets.available_amount + EXCLUDED.available_amount,
			total_earned = donebot_creator_wallets.total_earned + EXCLUDED.total_earned,
			updated_at = NOW()`,
		authorUserID,
		func() float64 {
			if status == "frozen" {
				return reward
			}
			return 0
		}(),
		func() float64 {
			if status == "available" {
				return reward
			}
			return 0
		}(),
		reward); err != nil {
		return err
	}

	_, _ = tx.ExecContext(ctx, `
		UPDATE donebot_market_installs
		SET last_used_at = NOW(),
		    total_cost = total_cost + $1,
		    total_reward = total_reward + $2,
		    updated_at = NOW()
		WHERE id = $3`, round6(baseCost), reward, installID)

	_, _ = tx.ExecContext(ctx, `
		UPDATE donebot_market_listings
		SET total_attributed_cost = total_attributed_cost + $1,
		    total_creator_reward = total_creator_reward + $2
		WHERE id = $3`, round6(baseCost), reward, listingID)

	return tx.Commit()
}

// --- Per-Bot Actual Spend Tracking (All User Bots, Independent of Market Rebates) ---

// RecordBotLLMSpend records an actual settled LLM cost for a user's bot (idempotent by usageKey).
func (s *Service) RecordBotLLMSpend(ctx context.Context, userID int64, botID, usageKey string, actualCost float64) error {
	if !s.Enabled() || userID <= 0 || strings.TrimSpace(botID) == "" || strings.TrimSpace(usageKey) == "" || actualCost <= 0 {
		return nil
	}
	cleanBotID := strings.TrimSpace(botID)
	if len(cleanBotID) > 128 {
		cleanBotID = cleanBotID[:128]
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO donebot_bot_spend_events (user_id, bot_id, category, cost_amount, usage_key)
		VALUES ($1, $2, 'llm', $3, $4)
		ON CONFLICT (user_id, category, usage_key) DO NOTHING`,
		userID, cleanBotID, round6(actualCost), "llm:"+strings.TrimSpace(usageKey))
	return err
}

// RecordBotMediaSpendRef records the owning bot_id for an admitted async media receipt.
func (s *Service) RecordBotMediaSpendRef(ctx context.Context, receiptID string, userID int64, botID, category string) {
	if !s.Enabled() || strings.TrimSpace(receiptID) == "" || userID <= 0 || strings.TrimSpace(botID) == "" {
		return
	}
	cat := normalizeMediaCategory(category)
	if cat == "" {
		return
	}
	cleanBotID := strings.TrimSpace(botID)
	if len(cleanBotID) > 128 {
		cleanBotID = cleanBotID[:128]
	}
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO donebot_bot_media_spend_refs (receipt_id, user_id, bot_id, category)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (receipt_id) DO NOTHING`,
		strings.TrimSpace(receiptID), userID, cleanBotID, cat)
}

// SettleBotMediaSpend records actual settled media spend for the bot associated with receiptID.
func (s *Service) SettleBotMediaSpend(ctx context.Context, receiptID string, userID int64, actualCost float64, failed bool) error {
	if !s.Enabled() || failed || strings.TrimSpace(receiptID) == "" || userID <= 0 || actualCost <= 0 {
		return nil
	}
	var botID, category string
	var storedUserID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, bot_id, category
		FROM donebot_bot_media_spend_refs
		WHERE receipt_id = $1`, strings.TrimSpace(receiptID)).Scan(&storedUserID, &botID, &category)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if storedUserID != userID || strings.TrimSpace(botID) == "" {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO donebot_bot_spend_events (user_id, bot_id, category, cost_amount, usage_key)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, category, usage_key) DO NOTHING`,
		userID, botID, category, round6(actualCost), "media:"+strings.TrimSpace(receiptID))
	return err
}

// GetUserBotSpendSummary returns real 4-category spend per bot for the last N days.
func (s *Service) GetUserBotSpendSummary(ctx context.Context, userID int64, days int) (*BotSpendSummary, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	since := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	rows, err := s.db.QueryContext(ctx, `
		SELECT bot_id,
		       COALESCE(SUM(cost_amount), 0) AS total_cost,
		       COALESCE(SUM(cost_amount) FILTER (WHERE category = 'llm'), 0) AS llm_cost,
		       COALESCE(SUM(cost_amount) FILTER (WHERE category = 'image'), 0) AS image_cost,
		       COALESCE(SUM(cost_amount) FILTER (WHERE category = 'video'), 0) AS video_cost,
		       COALESCE(SUM(cost_amount) FILTER (WHERE category = 'music'), 0) AS music_cost,
		       COUNT(*) AS event_count,
		       MAX(created_at) AS last_used_at
		FROM donebot_bot_spend_events
		WHERE user_id = $1 AND created_at >= $2
		GROUP BY bot_id
		ORDER BY total_cost DESC, last_used_at DESC
		LIMIT 200`, userID, since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	summary := &BotSpendSummary{
		Days: days,
		Bots: make([]BotSpendRow, 0),
	}
	for rows.Next() {
		var row BotSpendRow
		if err := rows.Scan(
			&row.BotID, &row.TotalCost, &row.LLMCost, &row.ImageCost,
			&row.VideoCost, &row.MusicCost, &row.EventCount, &row.LastUsedAt,
		); err != nil {
			return nil, err
		}
		row.TotalCost = round6(row.TotalCost)
		row.LLMCost = round6(row.LLMCost)
		row.ImageCost = round6(row.ImageCost)
		row.VideoCost = round6(row.VideoCost)
		row.MusicCost = round6(row.MusicCost)
		summary.TotalCost = round6(summary.TotalCost + row.TotalCost)
		summary.LLMCost = round6(summary.LLMCost + row.LLMCost)
		summary.ImageCost = round6(summary.ImageCost + row.ImageCost)
		summary.VideoCost = round6(summary.VideoCost + row.VideoCost)
		summary.MusicCost = round6(summary.MusicCost + row.MusicCost)
		summary.Bots = append(summary.Bots, row)
	}
	return summary, rows.Err()
}

// --- Freeze Thawing ---

func (s *Service) thawUserRewardsTx(ctx context.Context, tx *sql.Tx, userID int64) (float64, error) {
	var thawed float64
	err := tx.QueryRowContext(ctx, `
		WITH matured AS (
			UPDATE donebot_creator_reward_ledger
			SET status = 'available'
			WHERE creator_user_id = $1
			  AND entry_type = 'accrue'
			  AND status = 'frozen'
			  AND available_at <= NOW()
			RETURNING reward_amount
		)
		SELECT COALESCE(SUM(reward_amount), 0) FROM matured`, userID).Scan(&thawed)
	if err != nil {
		return 0, err
	}
	if thawed <= 0 {
		return 0, nil
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE donebot_creator_wallets
		SET frozen_amount = GREATEST(frozen_amount - $1, 0),
		    available_amount = available_amount + $1,
		    updated_at = NOW()
		WHERE user_id = $2`, thawed, userID)
	return thawed, err
}

func (s *Service) ThawAllMaturedRewards(ctx context.Context) (int, error) {
	if !s.Enabled() {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT creator_user_id
		FROM donebot_creator_reward_ledger
		WHERE entry_type = 'accrue' AND status = 'frozen' AND available_at <= NOW()
		LIMIT 200`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	userIDs := make([]int64, 0)
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err == nil {
			userIDs = append(userIDs, uid)
		}
	}
	count := 0
	for _, uid := range userIDs {
		tx, tErr := s.db.BeginTx(ctx, nil)
		if tErr != nil {
			continue
		}
		thawed, thErr := s.thawUserRewardsTx(ctx, tx, uid)
		if thErr == nil && thawed > 0 && tx.Commit() == nil {
			count++
		} else {
			_ = tx.Rollback()
		}
	}
	return count, nil
}

// --- Creator Wallet, Ledger, Transfer to Balance, Withdrawals ---

func (s *Service) GetCreatorWallet(ctx context.Context, userID int64) (*CreatorWallet, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `INSERT INTO donebot_creator_wallets (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return nil, err
	}
	if _, err := s.thawUserRewardsTx(ctx, tx, userID); err != nil {
		return nil, err
	}

	var w CreatorWallet
	err = tx.QueryRowContext(ctx, `
		SELECT user_id, frozen_amount, available_amount, total_earned, total_transferred, total_withdrawn, updated_at
		FROM donebot_creator_wallets WHERE user_id = $1`, userID).Scan(
		&w.UserID, &w.FrozenAmount, &w.AvailableAmount, &w.TotalEarned, &w.TotalTransferred, &w.TotalWithdrawn, &w.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_creator_withdrawals WHERE creator_user_id = $1 AND status = 'pending'`, userID).Scan(&w.PendingWithdrawCount)
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	w.MinWithdrawAmount = settings.MinWithdrawAmount
	w.FreezeDays = settings.FreezeDays
	w.TransferBonusPercent = settings.TransferBonusPercent
	w.RebateLLMPercent = settings.RebateLLMPercent
	w.RebateImagePercent = settings.RebateImagePercent
	w.RebateVideoPercent = settings.RebateVideoPercent
	w.RebateMusicPercent = settings.RebateMusicPercent
	return &w, nil
}

func (s *Service) ListCreatorLedger(ctx context.Context, userID int64, page, pageSize int) ([]RewardLedgerEntry, int, error) {
	if err := s.available(); err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_creator_reward_ledger WHERE creator_user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, COALESCE(r.listing_id::text, ''), COALESCE(l.name, ''),
		       r.entry_type, r.category, r.base_cost, r.rate_percent, r.reward_amount, r.bonus_amount,
		       r.status, r.available_at, r.note, r.created_at
		FROM donebot_creator_reward_ledger r
		LEFT JOIN donebot_market_listings l ON l.id = r.listing_id
		WHERE r.creator_user_id = $1
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $2 OFFSET $3`, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]RewardLedgerEntry, 0)
	for rows.Next() {
		var e RewardLedgerEntry
		if err := rows.Scan(&e.ID, &e.ListingID, &e.ListingName, &e.EntryType, &e.Category,
			&e.BaseCost, &e.RatePercent, &e.RewardAmount, &e.BonusAmount, &e.Status, &e.AvailableAt, &e.Note, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, e)
	}
	return items, total, rows.Err()
}

func (s *Service) TransferToBalance(ctx context.Context, userID int64, requestedAmount float64) (*TransferToBalanceResult, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `INSERT INTO donebot_creator_wallets (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return nil, err
	}
	if _, err := s.thawUserRewardsTx(ctx, tx, userID); err != nil {
		return nil, err
	}

	var available float64
	if err := tx.QueryRowContext(ctx, `SELECT available_amount FROM donebot_creator_wallets WHERE user_id = $1 FOR UPDATE`, userID).Scan(&available); err != nil {
		return nil, err
	}
	amount := round2(requestedAmount)
	if requestedAmount <= 0 {
		amount = round2(available)
	}
	if amount < MinTransferAmount {
		return nil, infraerrors.BadRequest("CREATOR_TRANSFER_TOO_SMALL", fmt.Sprintf("转入算力余额单次最低 %.2f 元。", MinTransferAmount))
	}
	if amount > available+1e-6 {
		return nil, infraerrors.BadRequest("CREATOR_QUOTA_INSUFFICIENT", "可用创作者奖励余额不足。")
	}
	if amount > available {
		amount = available
	}

	bonus := round6(amount * settings.TransferBonusPercent / 100.0)
	credit := round6(amount + bonus)

	var remaining float64
	if err := tx.QueryRowContext(ctx, `
		UPDATE donebot_creator_wallets
		SET available_amount = available_amount - $1,
		    total_transferred = total_transferred + $1,
		    updated_at = NOW()
		WHERE user_id = $2 AND available_amount >= $1
		RETURNING available_amount`, amount, userID).Scan(&remaining); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.BadRequest("CREATOR_QUOTA_INSUFFICIENT", "可用创作者奖励余额不足。")
		}
		return nil, err
	}

	var newBalance float64
	if err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING balance`, credit, userID).Scan(&newBalance); err != nil {
		return nil, err
	}

	note := fmt.Sprintf("创作者奖励转入算力余额（本金 %.2f", amount)
	if bonus > 0 {
		note += fmt.Sprintf(" + %.2f%% 加成 %.2f", settings.TransferBonusPercent, bonus)
	}
	note += "）"
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO donebot_creator_reward_ledger (
			creator_user_id, entry_type, category, source_key, base_cost, rate_percent,
			reward_amount, bonus_amount, status, note
		) VALUES ($1,'transfer_to_balance','system',$2,$3,$4,$5,$6,'settled',$7)`,
		userID, "transfer:"+uuid.NewString(), amount, settings.TransferBonusPercent, -amount, bonus, note); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if s.invalidateBalance != nil {
		s.invalidateBalance(ctx, userID)
	}
	return &TransferToBalanceResult{
		Amount:       amount,
		BonusAmount:  bonus,
		CreditAmount: credit,
		NewBalance:   newBalance,
		Available:    remaining,
	}, nil
}

func (s *Service) RequestWithdrawal(ctx context.Context, userID int64, requestedAmount float64, alipayAccount, alipayRealName string) (*WithdrawalRecord, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	account, err := cleanLine(alipayAccount, 120, "CREATOR_ALIPAY_INVALID", "请填写有效的支付宝账号。")
	if err != nil {
		return nil, err
	}
	realName, err := cleanLine(alipayRealName, 60, "CREATOR_NAME_INVALID", "请填写支付宝实名收款人姓名。")
	if err != nil {
		return nil, err
	}
	amount := round2(requestedAmount)
	if amount < settings.MinWithdrawAmount {
		return nil, infraerrors.BadRequest("CREATOR_WITHDRAW_BELOW_MIN", fmt.Sprintf("提现金额不能低于 %.2f 元。", settings.MinWithdrawAmount))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `INSERT INTO donebot_creator_wallets (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return nil, err
	}
	if _, err := s.thawUserRewardsTx(ctx, tx, userID); err != nil {
		return nil, err
	}

	var available float64
	if err := tx.QueryRowContext(ctx, `SELECT available_amount FROM donebot_creator_wallets WHERE user_id = $1 FOR UPDATE`, userID).Scan(&available); err != nil {
		return nil, err
	}
	var pendingCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_creator_withdrawals WHERE creator_user_id = $1 AND status = 'pending'`, userID).Scan(&pendingCount); err != nil {
		return nil, err
	}
	if pendingCount > 0 {
		return nil, infraerrors.Conflict("CREATOR_WITHDRAW_PENDING", "您已有一笔待审核的提现申请，请等待处理完成后再提交。")
	}
	if amount > available+1e-6 {
		return nil, infraerrors.BadRequest("CREATOR_QUOTA_INSUFFICIENT", "可用创作者奖励余额不足。")
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE donebot_creator_wallets
		SET available_amount = available_amount - $1, updated_at = NOW()
		WHERE user_id = $2 AND available_amount >= $1`, amount, userID); err != nil {
		return nil, err
	}

	var rec WithdrawalRecord
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO donebot_creator_withdrawals (creator_user_id, amount, alipay_account, alipay_real_name, status)
		VALUES ($1,$2,$3,$4,'pending')
		RETURNING id, creator_user_id, amount, alipay_account, alipay_real_name, status, payout_trade_no, admin_note, reviewed_at, created_at`,
		userID, amount, account, realName).Scan(
		&rec.ID, &rec.CreatorUserID, &rec.Amount, &rec.AlipayAccount, &rec.AlipayRealName,
		&rec.Status, &rec.PayoutTradeNo, &rec.AdminNote, &rec.ReviewedAt, &rec.CreatedAt); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO donebot_creator_reward_ledger (
			creator_user_id, entry_type, category, source_key, reward_amount, status, note
		) VALUES ($1,'withdraw_freeze','system',$2,$3,'frozen',$4)`,
		userID, fmt.Sprintf("withdraw:freeze:%d", rec.ID), -amount,
		fmt.Sprintf("申请支付宝提现 %.2f 元（%s）", amount, account)); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Service) ListCreatorWithdrawals(ctx context.Context, userID int64, page, pageSize int) ([]WithdrawalRecord, int, error) {
	return s.queryWithdrawals(ctx, userID, "", page, pageSize)
}

func (s *Service) AdminListWithdrawals(ctx context.Context, status string, page, pageSize int) ([]WithdrawalRecord, int, error) {
	return s.queryWithdrawals(ctx, 0, status, page, pageSize)
}

func (s *Service) queryWithdrawals(ctx context.Context, userID int64, status string, page, pageSize int) ([]WithdrawalRecord, int, error) {
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
	if userID > 0 {
		where = append(where, fmt.Sprintf("w.creator_user_id = $%d", idx))
		args = append(args, userID)
		idx++
	}
	if st := strings.TrimSpace(status); st != "" && st != "all" {
		where = append(where, fmt.Sprintf("w.status = $%d", idx))
		args = append(args, st)
		idx++
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM donebot_creator_withdrawals w WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT w.id, w.creator_user_id, u.email, COALESCE(NULLIF(u.username, ''), u.email),
		       w.amount, w.alipay_account, w.alipay_real_name, w.status,
		       w.payout_trade_no, w.admin_note, w.reviewed_at, w.created_at
		FROM donebot_creator_withdrawals w
		JOIN users u ON u.id = w.creator_user_id
		WHERE %s
		ORDER BY CASE WHEN w.status = 'pending' THEN 0 ELSE 1 END, w.created_at DESC
		LIMIT $%d OFFSET $%d`, whereSQL, idx, idx+1), append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]WithdrawalRecord, 0)
	for rows.Next() {
		var rec WithdrawalRecord
		if err := rows.Scan(&rec.ID, &rec.CreatorUserID, &rec.CreatorEmail, &rec.CreatorName,
			&rec.Amount, &rec.AlipayAccount, &rec.AlipayRealName, &rec.Status,
			&rec.PayoutTradeNo, &rec.AdminNote, &rec.ReviewedAt, &rec.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, rec)
	}
	return items, total, rows.Err()
}

func (s *Service) AdminProcessWithdrawal(ctx context.Context, adminUserID, withdrawalID int64, decision, tradeNo, adminNote string) (*WithdrawalRecord, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != "paid" && decision != "rejected" {
		return nil, infraerrors.BadRequest("CREATOR_WITHDRAW_DECISION_INVALID", "审核状态仅支持 paid 或 rejected。")
	}
	tradeNo = strings.TrimSpace(tradeNo)
	adminNote = strings.TrimSpace(adminNote)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var rec WithdrawalRecord
	err = tx.QueryRowContext(ctx, `
		SELECT id, creator_user_id, amount, alipay_account, alipay_real_name, status, payout_trade_no, admin_note, reviewed_at, created_at
		FROM donebot_creator_withdrawals WHERE id = $1 FOR UPDATE`, withdrawalID).Scan(
		&rec.ID, &rec.CreatorUserID, &rec.Amount, &rec.AlipayAccount, &rec.AlipayRealName,
		&rec.Status, &rec.PayoutTradeNo, &rec.AdminNote, &rec.ReviewedAt, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CREATOR_WITHDRAW_NOT_FOUND", "提现申请不存在。")
	}
	if err != nil {
		return nil, err
	}
	if rec.Status != "pending" {
		return nil, infraerrors.Conflict("CREATOR_WITHDRAW_PROCESSED", "该提现申请已处理，无法重复操作。")
	}

	if err := tx.QueryRowContext(ctx, `
		UPDATE donebot_creator_withdrawals
		SET status = $1, payout_trade_no = $2, admin_note = $3, reviewed_by = $4, reviewed_at = NOW()
		WHERE id = $5
		RETURNING status, payout_trade_no, admin_note, reviewed_at`,
		decision, tradeNo, adminNote, adminUserID, withdrawalID).Scan(&rec.Status, &rec.PayoutTradeNo, &rec.AdminNote, &rec.ReviewedAt); err != nil {
		return nil, err
	}

	if decision == "paid" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE donebot_creator_wallets
			SET total_withdrawn = total_withdrawn + $1, updated_at = NOW()
			WHERE user_id = $2`, rec.Amount, rec.CreatorUserID); err != nil {
			return nil, err
		}
		_, _ = tx.ExecContext(ctx, `
			UPDATE donebot_creator_reward_ledger SET status = 'settled', note = $1
			WHERE source_key = $2`,
			fmt.Sprintf("支付宝提现已打款 %.2f 元（流水号：%s）", rec.Amount, tradeNo),
			fmt.Sprintf("withdraw:freeze:%d", rec.ID))
	} else {
		// Refund back to available balance atomically.
		if _, err := tx.ExecContext(ctx, `
			UPDATE donebot_creator_wallets
			SET available_amount = available_amount + $1, updated_at = NOW()
			WHERE user_id = $2`, rec.Amount, rec.CreatorUserID); err != nil {
			return nil, err
		}
		_, _ = tx.ExecContext(ctx, `
			UPDATE donebot_creator_reward_ledger SET status = 'reversed'
			WHERE source_key = $1`, fmt.Sprintf("withdraw:freeze:%d", rec.ID))
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO donebot_creator_reward_ledger (
				creator_user_id, entry_type, category, source_key, reward_amount, status, note
			) VALUES ($1,'withdraw_refund','system',$2,$3,'available',$4)
			ON CONFLICT (source_key) DO NOTHING`,
			rec.CreatorUserID, fmt.Sprintf("withdraw:refund:%d", rec.ID), rec.Amount,
			fmt.Sprintf("提现申请被驳回退回 %.2f 元（%s）", rec.Amount, adminNote)); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &rec, nil
}
