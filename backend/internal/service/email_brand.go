package service

import (
	_ "embed"
	"encoding/base64"
	"html"
	"log/slog"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const doneBotEmailLogoCID = "donebot-logo@donebot.cn"

//go:embed email_assets/donebot-logo.png
var doneBotEmailLogo []byte

func notificationEmailCard(locale, title, content string) string {
	return brandedEmailDocument(locale, "{{site_name}}", title, content)
}

func brandedEmailDocument(locale, siteName, title, content string) string {
	lang, label := "en", "Notification"
	footer := "This email was sent automatically by " + html.EscapeString(siteName) + ". Please do not reply."
	if normalizeNotificationLocale(locale) == notificationEmailLocaleChinese {
		lang, label = "zh-CN", "系统通知"
		footer = "此邮件由 " + html.EscapeString(siteName) + " 自动发送，请勿直接回复。"
	}
	return `<!DOCTYPE html>
<html lang="` + lang + `">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>` + html.EscapeString(title) + ` · DoneBot</title>
<style>
body, table, td, p, a { letter-spacing:0; }
table { mso-table-lspace:0pt; mso-table-rspace:0pt; }
@media only screen and (max-width:620px) {
  .email-outer { padding:16px 8px !important; }
  .email-header, .email-content, .email-footer { padding-left:22px !important; padding-right:22px !important; }
  .email-content h1 { font-size:23px !important; }
}
</style>
</head>
<body style="margin:0;padding:0;width:100%;background-color:#f3f4f5;color:#202126;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',Arial,sans-serif;letter-spacing:0;-webkit-text-size-adjust:100%;">
<div style="display:none;font-size:1px;line-height:1px;max-height:0;max-width:0;opacity:0;overflow:hidden;mso-hide:all;">` + html.EscapeString(title) + ` · ` + html.EscapeString(siteName) + `</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;background-color:#f3f4f5;">
<tr><td class="email-outer" align="center" style="padding:40px 16px;">
<!--[if mso]><table role="presentation" width="600" cellpadding="0" cellspacing="0"><tr><td><![endif]-->
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;table-layout:fixed;background-color:#ffffff;border:1px solid #e4e5e8;border-top:3px solid #c92e3c;border-radius:8px;">
<tr><td class="email-header" style="padding:30px 36px 28px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0"><tr>
<td width="48" style="width:48px;vertical-align:middle;"><img src="cid:` + doneBotEmailLogoCID + `" width="40" height="40" alt="" style="display:block;border:0;border-radius:6px;width:40px;height:40px;"></td>
<td style="font-size:24px;font-weight:700;line-height:32px;color:#202126;vertical-align:middle;">DoneBot</td>
<td align="right" style="font-size:12px;line-height:20px;color:#64676e;vertical-align:middle;">` + label + `</td>
</tr></table>
</td></tr>
<tr><td class="email-content" style="padding:0 36px 28px;font-size:15px;line-height:1.8;color:#44474f;overflow-wrap:anywhere;word-break:break-word;">
<h1 style="margin:0 0 24px;font-size:24px;line-height:1.4;font-weight:700;color:#202126;">` + html.EscapeString(title) + `</h1>
` + styleEmailContent(content) + `
</td></tr>
<tr><td class="email-footer" style="padding:20px 36px 24px;border-top:1px solid #eceef0;font-size:12px;line-height:1.8;color:#64676e;overflow-wrap:anywhere;">` + footer + `</td></tr>
</table>
<!--[if mso]></td></tr></table><![endif]-->
</td></tr></table>
</body></html>`
}

func styleEmailContent(content string) string {
	nodes, err := xhtml.ParseFragment(strings.NewReader(content), &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		return content
	}
	var styleNode func(*xhtml.Node)
	styleNode = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			style := ""
			switch n.Data {
			case "p":
				style = "margin:0 0 16px;"
			case "a":
				style = "color:#c92e3c;text-decoration:underline;overflow-wrap:anywhere;word-break:break-all;"
			case "h2", "h3":
				style = "margin:26px 0 12px;font-size:16px;line-height:1.5;color:#202126;"
			case "table":
				style = "width:100%;border-collapse:collapse;table-layout:fixed;margin:20px 0;"
			case "td", "th":
				style = "padding:11px 0;border-bottom:1px solid #eceef0;vertical-align:top;text-align:left;font-size:13px;line-height:1.7;overflow-wrap:anywhere;word-break:break-word;"
				if emailElementCount(n.Parent) > 2 {
					style += "padding:10px 6px;color:#44474f;font-size:12px;"
				} else if emailFirstElement(n.Parent) == n {
					style += "width:40%;padding-right:16px;color:#64676e;"
				} else {
					style += "color:#202126;font-weight:600;"
				}
			case "li":
				style = "margin-bottom:8px;"
			}
			for _, attr := range n.Attr {
				if attr.Key != "class" {
					continue
				}
				for _, class := range strings.Fields(attr.Val) {
					switch class {
					case "button":
						style = "display:inline-block;background-color:#c92e3c;border:1px solid #c92e3c;border-radius:6px;color:#ffffff;font-size:14px;font-weight:600;line-height:22px;text-align:center;text-decoration:none;padding:12px 24px;mso-padding-alt:12px 24px;"
					case "muted":
						style += "font-size:13px;line-height:1.8;color:#64676e;overflow-wrap:anywhere;word-break:break-word;"
					case "verification-code":
						style += "background-color:#f3f4f5;border:1px solid #e4e5e8;border-radius:6px;padding:20px 12px;margin:22px 0;text-align:center;font-family:'SFMono-Regular',Consolas,'Liberation Mono',monospace;font-size:34px;font-weight:700;line-height:1.4;letter-spacing:0;color:#202126;"
					case "metric-label":
						style += "display:block;color:#64676e;font-size:12px;font-weight:400;"
					case "metric-value":
						style += "display:block;margin-top:4px;color:#202126;font-size:22px;font-weight:700;"
					case "metric-cell":
						style = "width:50%;padding:16px;background-color:#f3f4f5;border:3px solid #ffffff;vertical-align:top;overflow-wrap:anywhere;"
					case "alert":
						style += "color:#c92e3c;"
					case "good":
						style += "color:#237854;"
					}
				}
			}
			if style != "" {
				found := false
				for i := range n.Attr {
					if n.Attr[i].Key == "style" {
						n.Attr[i].Val = style + n.Attr[i].Val
						found = true
						break
					}
				}
				if !found {
					n.Attr = append(n.Attr, xhtml.Attribute{Key: "style", Val: style})
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			styleNode(child)
		}
	}
	var result strings.Builder
	for _, node := range nodes {
		styleNode(node)
		if err := xhtml.Render(&result, node); err != nil {
			return content
		}
	}
	return result.String()
}

func emailFirstElement(n *xhtml.Node) *xhtml.Node {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode {
			return child
		}
	}
	return nil
}

func emailElementCount(n *xhtml.Node) int {
	count := 0
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode {
			count++
		}
	}
	return count
}

func emailPreviewHTML(body string) string {
	return strings.ReplaceAll(body, "cid:"+doneBotEmailLogoCID, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(doneBotEmailLogo))
}

func defaultNotificationEmailBody(event, locale string, variables map[string]string) string {
	locale = normalizeNotificationLocale(locale)
	tmpl := notificationEmailOfficialTemplates[event][locale]
	if variables["recipient_name"] == "" {
		if locale == notificationEmailLocaleChinese {
			variables["recipient_name"] = "用户"
		} else {
			variables["recipient_name"] = "there"
		}
	}
	preview, err := renderNotificationEmail(event, tmpl.Subject, tmpl.HTML, variables, nil)
	if err != nil {
		slog.Error("failed to render default notification email", "event", event, "error", err)
		return ""
	}
	return preview.HTML
}

// BuildSMTPTestEmailBody builds the branded SMTP configuration test message.
func BuildSMTPTestEmailBody(siteName string) string {
	return brandedEmailDocument("zh", siteName, "邮件服务已就绪", `<p>这是一封 DoneBot 邮件服务测试通知。</p><p>收到此邮件，表示当前 SMTP 配置已成功完成发送。</p><p class="muted">This test message confirms that your SMTP settings can send email successfully.</p>`)
}
