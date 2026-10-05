package notification

import "strings"

func buildSecurityHTML(code string) string {
	var builder strings.Builder
	builder.WriteString(`<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body style="margin:0;padding:0;background:#f4f7fb;color:#172033;font-family:Arial,'Microsoft YaHei',sans-serif;line-height:1.6;">
<div style="padding:32px 16px;">
<div style="max-width:600px;margin:0 auto;background:#ffffff;border:1px solid #e4e9f2;border-radius:16px;overflow:hidden;">
<div style="padding:24px;background:#14213d;color:#ffffff;">
<div style="font-size:13px;">SCERT 证书管理服务</div>
<h1 style="margin:8px 0 0;font-size:24px;">超级管理员密码重置验证</h1>
</div>
<div style="padding:24px;">
<p style="margin:0 0 20px;">有人正在部署服务器上重置 Scert 超级管理员密码。</p>
<div style="padding:20px 12px;border:1px solid #d0d5dd;border-radius:12px;background:#f8fafc;text-align:center;">
<div style="font-size:14px;color:#667085;">本次验证码</div>
<div style="margin-top:8px;font-size:30px;font-weight:700;letter-spacing:4px;color:#14213d;">`)
	builder.WriteString(escapeHTML(code))
	builder.WriteString(`</div>
</div>
<p style="margin:20px 0;">请在服务器本机重置终端输入。验证码仅在当前本机重置会话中有效，有效期不超过<strong>两分钟</strong>。</p>
<div style="padding:16px;border-left:4px solid #f79009;border-radius:8px;background:#fffaeb;color:#93370d;">如果不是您本人操作，请勿提供此验证码，并检查服务器访问权限。</div>
</div>
<div style="padding:16px 24px;background:#f8fafc;color:#667085;font-size:12px;">此邮件由 Scert 自动发送，请勿直接回复。</div>
</div>
</div>
</body>
</html>`)
	return builder.String()
}
