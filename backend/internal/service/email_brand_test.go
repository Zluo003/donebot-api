//go:build unit

package service

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDoneBotOfficialEmailTemplates(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	require.NoError(t, repo.Set(ctx, SettingKeySiteName, "DoneBot"))
	svc := NewNotificationEmailService(repo, nil)
	for _, event := range notificationEmailEventOrder {
		for _, locale := range []string{"zh", "en"} {
			t.Run(event+"/"+locale, func(t *testing.T) {
				tmpl, err := svc.GetTemplate(ctx, event, locale)
				require.NoError(t, err)
				require.NoError(t, validateNotificationEmailTemplate(event, tmpl.Subject, tmpl.HTML))
				require.Contains(t, tmpl.HTML, "cid:"+doneBotEmailLogoCID)
				require.NotContains(t, tmpl.HTML, "data:image")
				require.NotContains(t, tmpl.HTML, "linear-gradient")
				preview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{Event: event, Locale: locale})
				require.NoError(t, err)
				require.Contains(t, preview.HTML, "data:image/png;base64,")
				require.NotContains(t, preview.HTML, "cid:")
				require.Empty(t, notificationEmailPlaceholdersIn(preview.HTML))
				require.Contains(t, preview.HTML, "#c92e3c")
				if locale == "zh" {
					require.Contains(t, preview.HTML, `<html lang="zh-CN">`)
					require.Contains(t, preview.HTML, "此邮件由 DoneBot 自动发送")
				} else {
					require.Contains(t, preview.HTML, `<html lang="en">`)
					require.Contains(t, preview.HTML, "sent automatically by DoneBot")
				}
			})
		}
	}
}

func TestDoneBotEmailFallbacksEscapeDynamicContent(t *testing.T) {
	unsafe := `<script>alert("test")</script>`
	log := &ContentModerationLog{UserEmail: unsafe, GroupName: unsafe, HighestCategory: unsafe, Model: unsafe, Error: unsafe, AutoBanned: true}
	balance := &BalanceNotifyService{}
	bodies := []string{
		buildNotifyVerifyEmailBody(unsafe, unsafe),
		balance.buildBalanceLowEmailBody(unsafe, 1, 5, unsafe, "https://example.com/pay"),
		balance.buildQuotaAlertEmailBody(1, unsafe, unsafe, unsafe, 1, 5, 4, unsafe, unsafe),
		buildContentModerationViolationEmailBody(unsafe, log, nil),
		buildContentModerationAccountDisabledEmailBody(unsafe, log, nil),
		buildCyberPolicyNoticeEmailBody(unsafe, log),
		BuildSMTPTestEmailBody(unsafe),
		buildOpsAlertEmailBody(&OpsAlertRule{Name: unsafe}, &OpsAlertEvent{Description: unsafe}, unsafe),
	}
	for i, body := range bodies {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			require.NotEmpty(t, body)
			require.NotContains(t, body, "<script>")
			require.Contains(t, body, html.EscapeString(unsafe))
			require.NotContains(t, body, "&amp;lt;")
			require.Contains(t, body, "cid:"+doneBotEmailLogoCID)
		})
	}
	require.Contains(t, bodies[3], "账户当前处于封禁状态")
	require.Contains(t, bodies[4], "账户当前处于封禁状态")
	require.NotContains(t, balance.buildBalanceLowEmailBody("User", 1, 5, "DoneBot", "javascript:alert(1)"), "javascript:")
}

func TestDoneBotEmailPreviewPreservesCustomTemplate(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)
	_, err := svc.UpdateTemplate(ctx, NotificationEmailEventAuthVerifyCode, "zh", "自定义", `<p>自定义：{{verification_code}}</p>`)
	require.NoError(t, err)
	preview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{Event: NotificationEmailEventAuthVerifyCode, Locale: "zh", Variables: map[string]string{"verification_code": "123456"}})
	require.NoError(t, err)
	require.Equal(t, `<p>自定义：123456</p>`, preview.HTML)
}

func TestDoneBotEmailPreviewGallery(t *testing.T) {
	dir := os.Getenv("DONEBOT_EMAIL_PREVIEW_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	require.NoError(t, os.MkdirAll(dir, 0755))
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	require.NoError(t, repo.Set(ctx, SettingKeySiteName, "DoneBot"))
	svc := NewNotificationEmailService(repo, nil)
	for _, event := range notificationEmailEventOrder {
		for _, locale := range []string{"zh", "en"} {
			preview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{Event: event, Locale: locale, Variables: map[string]string{"recipient_name": "Alex"}})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, event+"."+locale+".html"), []byte(preview.HTML), 0644))
		}
	}
	longPreview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{Event: NotificationEmailEventCyberPolicyNotice, Locale: "en", Variables: map[string]string{
		"upstream_message": strings.Repeat("long-upstream-message-", 100),
	}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "long-message.html"), []byte(longPreview.HTML), 0644))
	digest := buildOpsErrorDigestEmailHTML("Error digest", time.Now(), time.Now(), &OpsErrorLogList{Total: 1, Errors: []*OpsErrorLog{{Message: strings.Repeat("long-message-", 30), Platform: "Example", StatusCode: 502}}})
	digestBody := emailPreviewHTML(brandedEmailDocument("en", "DoneBot", "Error digest", digest))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "error-digest.html"), []byte(digestBody), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "smtp-test.html"), []byte(emailPreviewHTML(BuildSMTPTestEmailBody("DoneBot"))), 0644))
	gallery, err := os.ReadFile("testdata/email_preview_gallery.html")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), gallery, 0644))
}
