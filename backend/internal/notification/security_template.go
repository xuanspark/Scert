package notification

import "strings"

// buildSecurityHTML 构建超级管理员重置安全验证码邮件 HTML内容
func buildSecurityHTML(code string) string {
	var builder strings.Builder
	beginHTMLMail(&builder, "邮箱验证码")
	builder.WriteString(`<p style="margin:0 0 16px;font-size:15px;color:#18181b;">您好，有人正在请求重置 Scert 超级管理员密码。</p>`)
	builder.WriteString(`<p style="margin:0 0 12px;font-size:14px;color:#71717a;">您的验证码是：</p>`)
	builder.WriteString(`<div style="margin:18px 0;padding:24px 16px;background:#fafafa;border:1px solid #e4e4e7;border-radius:10px;text-align:center;"><div style="font-size:36px;font-weight:800;letter-spacing:10px;color:#4f46e5;font-family:Menlo,Monaco,Consolas,monospace;">`)
	builder.WriteString(escapeHTML(code))
	builder.WriteString(`</div></div>`)
	builder.WriteString(`<p style="margin:16px 0;font-size:14px;color:#3f3f46;">请在服务器本机重置终端中输入。验证码将在 <strong style="color:#18181b;">两分钟</strong>后失效。</p>`)
	builder.WriteString(`<div style="margin-top:20px;padding:14px 16px;border-left:4px solid #f59e0b;border-radius:8px;background:#fffbeb;color:#92400e;font-size:13px;line-height:1.5;">如果不是您本人操作，请勿提供此验证码，并检查服务器访问权限。</div>`)
	endHTMLMail(&builder)
	return builder.String()
}
