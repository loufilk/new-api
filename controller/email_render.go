package controller

import (
	"fmt"
	"html"
	"strings"
)

// authEmailContent describes one account email: either a verification code or
// a password-reset link. Both flows share one branded layout, so the layout
// lives here and each flow only supplies its wording and payload.
type authEmailContent struct {
	Heading         string
	Intro           string // sentence without the greeting, e.g. 您正在进行%s邮箱验证。
	Code            string // verification code; empty for link emails
	Link            string // action link; empty for code emails
	ActionLabel     string // link button label
	ValidityLabel   string // 验证码 or 重置链接
	ValidityMinutes int
	SystemName      string
	PublicHost      string
}

// renderAuthEmail returns the plain-text and HTML bodies for an account email.
// Every interpolated value is HTML-escaped and the plain-text variant is sent
// alongside the HTML so clients without HTML rendering still work.
func renderAuthEmail(mail authEmailContent) (string, string) {
	var plainText strings.Builder
	fmt.Fprintf(&plainText, "您好：\n\n%s\n\n", mail.Intro)
	if mail.Code != "" {
		fmt.Fprintf(&plainText, "验证码：%s\n\n", mail.Code)
	} else {
		fmt.Fprintf(&plainText, "请在浏览器中打开以下链接：\n%s\n\n", mail.Link)
	}
	fmt.Fprintf(&plainText, "%s %d 分钟内有效。若非本人操作，请忽略本邮件。\n\n%s\n%s",
		mail.ValidityLabel, mail.ValidityMinutes, mail.SystemName, mail.PublicHost)

	var body strings.Builder
	body.WriteString(`<!doctype html><html><body style="margin:0;padding:24px;background:#f6f8fb;color:#1f2937;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',Arial,sans-serif"><main style="max-width:560px;margin:0 auto;background:#ffffff;border:1px solid #e5e7eb;border-radius:12px;padding:28px"><h1 style="margin:0 0 20px;font-size:22px">`)
	body.WriteString(html.EscapeString(mail.Heading))
	body.WriteString(`</h1><p style="line-height:1.7">您好，`)
	body.WriteString(html.EscapeString(mail.Intro))
	body.WriteString(`</p>`)
	if mail.Code != "" {
		body.WriteString(`<p style="margin:24px 0;padding:16px;background:#f3f4f6;border-radius:8px;text-align:center;font-size:30px;font-weight:700;letter-spacing:6px;color:#111827">`)
		body.WriteString(html.EscapeString(mail.Code))
		body.WriteString(`</p>`)
	} else {
		body.WriteString(`<p style="margin:24px 0"><a href="`)
		body.WriteString(html.EscapeString(mail.Link))
		body.WriteString(`" style="display:inline-block;padding:12px 22px;border-radius:8px;background:#111827;color:#ffffff;text-decoration:none;font-weight:600">`)
		body.WriteString(html.EscapeString(mail.ActionLabel))
		body.WriteString(`</a></p><p style="line-height:1.7">如果按钮无法打开，请复制以下地址到浏览器：</p><p style="padding:12px;background:#f3f4f6;border-radius:8px;word-break:break-all;font-size:13px">`)
		body.WriteString(html.EscapeString(mail.Link))
		body.WriteString(`</p>`)
	}
	fmt.Fprintf(&body, `<p style="line-height:1.7">%s %d 分钟内有效。若非本人操作，请忽略本邮件。</p><hr style="margin:24px 0;border:0;border-top:1px solid #e5e7eb"><p style="margin:0;color:#6b7280;font-size:13px">%s · %s</p></main></body></html>`,
		html.EscapeString(mail.ValidityLabel), mail.ValidityMinutes, html.EscapeString(mail.SystemName), html.EscapeString(mail.PublicHost))
	return plainText.String(), body.String()
}
