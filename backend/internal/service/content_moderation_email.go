package service

import (
	"fmt"
	"html"
	"strings"
	"time"
)

func buildContentModerationViolationEmailBody(siteName string, log *ContentModerationLog, cfg *ContentModerationConfig) string {
	return buildContentModerationFallbackEmail(siteName, log, cfg, false)
}

func buildContentModerationAccountDisabledEmailBody(siteName string, log *ContentModerationLog, cfg *ContentModerationConfig) string {
	return buildContentModerationFallbackEmail(siteName, log, cfg, true)
}

func buildContentModerationFallbackEmail(siteName string, log *ContentModerationLog, cfg *ContentModerationConfig, disabled bool) string {
	if log == nil {
		return ""
	}
	userName := strings.TrimSpace(log.UserEmail)
	if userName == "" && log.UserID != nil {
		userName = fmt.Sprintf("UID %d", *log.UserID)
	}
	threshold := defaultContentModerationBanThreshold
	if cfg != nil && cfg.BanThreshold > 0 {
		threshold = cfg.BanThreshold
	}
	title, message := "账户风控提醒", "您的 API 请求触发了平台内容审核/风控策略。请检查请求内容，避免后续服务受到影响。"
	if disabled {
		title, message = "账户已被自动禁用", "您的账户多次触发平台内容审核/风控规则，系统已自动禁用该账户。如需申诉或恢复账号，请联系平台管理员处理。"
	}
	detailTitle := "触发详情"
	if disabled {
		detailTitle = "封禁详情"
	}
	content := fmt.Sprintf(`<p>%s，您好：</p><p>%s</p><h2>%s</h2>
<table>
<tr><td>触发时间</td><td>%s</td></tr>
<tr><td>所属分组</td><td>%s</td></tr>
<tr><td>命中类别 / 分数</td><td>%s / %.3f</td></tr>
<tr><td>累计触发次数</td><td>%d 次（阈值 %d）</td></tr>
	</table>`, html.EscapeString(userName), message, detailTitle,
		html.EscapeString(time.Now().Format("2006-01-02 15:04:05")),
		html.EscapeString(defaultContentModerationString(log.GroupName, "-")),
		html.EscapeString(defaultContentModerationString(log.HighestCategory, "-")),
		log.HighestScore, log.ViolationCount, threshold)
	if disabled || log.AutoBanned {
		content += `<p style="padding:16px;background-color:#fff2f3;border-left:3px solid #c92e3c;color:#a5212d;font-weight:600;">账户当前处于封禁状态，所有 API 请求将被拒绝。</p>`
	}
	return brandedEmailDocument("zh", siteName, title, content)
}

func buildCyberPolicyNoticeEmailBody(siteName string, log *ContentModerationLog) string {
	if log == nil {
		return ""
	}
	userName := strings.TrimSpace(log.UserEmail)
	if userName == "" && log.UserID != nil {
		userName = fmt.Sprintf("UID %d", *log.UserID)
	}
	return defaultNotificationEmailBody(NotificationEmailEventCyberPolicyNotice, "zh", map[string]string{
		"site_name": siteName, "recipient_name": userName,
		"triggered_at":     log.CreatedAt.Format("2006-01-02 15:04:05"),
		"model":            defaultContentModerationString(log.Model, "-"),
		"group_name":       defaultContentModerationString(log.GroupName, "-"),
		"upstream_message": defaultContentModerationString(log.Error, "-"),
	})
}

func defaultContentModerationString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
