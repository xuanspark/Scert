package notification

import (
	"context"
	"errors"
	"html"
	"net/mail"
	"strings"
)

// SendSecurityCode 仅向绑定邮箱发送验证码，不使用证书通知的收件人列表。
func (s *Service) SendSecurityCode(ctx context.Context, recipient, code string) error {
	if s == nil || (!s.config.HTTPEnabled && !s.config.SMTPEnabled) {
		return errors.New("未配置可用的验证码邮件通道")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(code) != 8 {
		return errors.New("验证码格式无效")
	}
	for _, value := range code {
		if value < '0' || value > '9' {
			return errors.New("验证码格式无效")
		}
	}
	from := strings.TrimSpace(s.config.SenderEmail)
	for _, value := range []string{from, recipient} {
		parsed, err := mail.ParseAddress(value)
		if err != nil || parsed.Address != value {
			return errors.New("验证码邮件地址无效")
		}
	}
	text := "有人正在部署服务器上重置 Scert 超级管理员密码。\n验证码：" + code +
		"\n验证码仅在当前本机重置会话中有效，有效期不超过两分钟。\n如果不是您本人操作，请勿提供此验证码，并检查服务器访问权限。"
	payload := mailPayload{
		From:    address{Name: s.config.SenderName, Email: from},
		To:      []address{{Email: recipient}},
		Subject: "[Scert] 超级管理员密码重置验证",
		Text:    text, HTML: "<pre>" + html.EscapeString(text) + "</pre>",
		CC: []address{}, BCC: []address{}, Attrs: []any{},
	}
	if s.config.HTTPEnabled {
		if err := s.sendHTTPPayload(ctx, payload); err == nil {
			return nil
		}
	}
	if s.config.SMTPEnabled {
		if err := s.sendSMTPWithRetry(ctx, payload); err == nil {
			return nil
		}
	}
	return errors.New("验证码邮件发送失败，请检查通知通道配置")
}
