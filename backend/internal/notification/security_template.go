package notification

import "strings"

func buildSecurityHTML(code string) string {
	var builder strings.Builder
	beginHTMLMail(&builder, "邮箱验证码")
	builder.WriteString(`<p style="margin:0 0 20px;">您好，有人正在请求重置 Scert 超级管理员密码。</p>`)
	builder.WriteString(`<div style="padding:20px 12px;border:1px solid #e4e4e7;border-radius:8px;background:#fafafa;text-align:center;"><div style="font-size:14px;color:#71717a;">本次验证码</div><div style="margin-top:8px;font-size:32px;font-weight:700;letter-spacing:8px;color:#4f46e5;">`)
	builder.WriteString(escapeHTML(code))
	builder.WriteString(`</div></div><p style="margin:20px 0;">请在服务器本机重置终端中输入。验证码仅在当前本机重置会话中有效，有效期不超过<strong>两分钟</strong>。</p><div style="padding:16px;border-left:4px solid #f59e0b;border-radius:8px;background:#fffbeb;color:#92400e;">如果不是您本人操作，请勿提供此验证码，并检查服务器访问权限。</div>`)
	endHTMLMail(&builder)
	return builder.String()
}
