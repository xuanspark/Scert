package notification

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// SendTestMail 发送测试邮件，验证发信通道配置与网络连通性。
func (s *Service) SendTestMail(ctx context.Context, targetRecipient string) error {
	if s == nil || (!s.config.HTTPEnabled && !s.config.SMTPEnabled) {
		return errors.New("发信服务未启用或未正确配置")
	}
	targetRecipient = strings.TrimSpace(targetRecipient)
	if targetRecipient == "" {
		if len(s.config.Recipients) > 0 {
			targetRecipient = s.config.Recipients[0]
		}
	}
	if targetRecipient == "" {
		return errors.New("收件人邮箱不能为空")
	}
	parsed, err := mail.ParseAddress(targetRecipient)
	if err != nil || parsed.Address != targetRecipient {
		return errors.New("测试邮件收件人地址格式无效")
	}

	fromName, from, err := s.config.SenderIdentity()
	if err != nil {
		return err
	}

	nowStr := time.Now().Format("2006-01-02 15:04:05")
	hostInfo := s.config.SMTPHost
	if s.config.SMTPPort > 0 {
		hostInfo = fmt.Sprintf("%s:%d", hostInfo, s.config.SMTPPort)
	}

	text := fmt.Sprintf("Scert 邮件通知通道测试成功\n\n发信测试已完成，您的邮件通知配置有效且能正常送达。\n发信主机：%s\n发信账号：%s\n测试时间：%s\n\n此邮件为系统自动测试邮件，无需回复。",
		hostInfo, s.config.SMTPUsername, nowStr)

	var htmlBuilder strings.Builder
	beginHTMLMail(&htmlBuilder, "邮件通道测试成功")
	htmlBuilder.WriteString(`<div style="display:inline-block;padding:4px 12px;border-radius:999px;background:#16803c;color:#ffffff;font-size:13px;font-weight:700;">测试通过</div>`)
	htmlBuilder.WriteString(`<h2 style="margin:14px 0 6px;font-size:24px;line-height:1.35;color:#18181b;">发信通道联通成功</h2>`)
	htmlBuilder.WriteString(`<p style="margin:0 0 24px;color:#71717a;font-size:14px;">您的 Scert 邮件通知配置已通过在线发信验证，可正常发送通知邮件。</p>`)

	htmlBuilder.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;border:1px solid #e4e4e7;border-radius:8px;overflow:hidden;font-size:14px;">`)
	writeHTMLField(&htmlBuilder, "SMTP 主机", hostInfo)
	if s.config.SMTPUsername != "" {
		writeHTMLField(&htmlBuilder, "发信账号", s.config.SMTPUsername)
	}
	writeHTMLField(&htmlBuilder, "连接模式", s.config.SMTPMode)
	writeHTMLField(&htmlBuilder, "目标收件人", targetRecipient)
	writeHTMLField(&htmlBuilder, "验证时间", nowStr)
	htmlBuilder.WriteString(`</table>`)

	htmlBuilder.WriteString(`<div style="margin-top:20px;padding:14px 16px;border-left:4px solid #16803c;border-radius:8px;background:#f0fdf4;color:#166534;font-size:13px;line-height:1.5;">系统已确认该发信通道有效，您可以放心保存当前系统邮件通知设置。</div>`)
	endHTMLMail(&htmlBuilder)

	payload := mailPayload{
		From:    address{Name: fromName, Email: from},
		To:      []address{{Email: targetRecipient}},
		Subject: "[Scert] 邮件通知配置测试成功",
		Text:    text,
		HTML:    htmlBuilder.String(),
		CC:      []address{},
		BCC:     []address{},
		Attrs:   []any{},
	}

	if s.config.UsesSmailSystemAccount() {
		if !s.config.SMTPEnabled {
			return errors.New("Smail 系统通知账号必须启用 SMTP 通道")
		}
		if err := s.sendSMTPWithRetry(ctx, payload); err != nil {
			return fmt.Errorf("SMTP 测试邮件发送失败：%w", err)
		}
		return nil
	}
	if s.config.HTTPEnabled {
		if err := s.sendHTTPPayload(ctx, payload); err == nil {
			return nil
		}
	}
	if s.config.SMTPEnabled {
		if err := s.sendSMTPWithRetry(ctx, payload); err != nil {
			return fmt.Errorf("SMTP 测试邮件发送失败：%w", err)
		}
		return nil
	}
	return errors.New("测试邮件发送失败，请检查通道设置及网络连通性")
}
