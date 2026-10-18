package config

import (
	"errors"
	"net/mail"
	"strings"
)

// UsesSmailSystemAccount 判断通知账号是否为 Smail 系统通知账号。
func (c NotificationConfig) UsesSmailSystemAccount() bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.SMTPUsername)), "notify:")
}

// SenderIdentity 根据用户配置或通知账号确定发件人。
// 遵循“用户怎么填就用什么”的原则，原样使用用户填写的发件人或发信账号，不擅自拼接或篡改发信地址。
func (c NotificationConfig) SenderIdentity() (string, string, error) {
	name := strings.TrimSpace(c.SenderName)
	email := strings.TrimSpace(c.SenderEmail)
	if !c.SMTPEnabled {
		return name, email, nil
	}
	// 如果用户明确填写了发件人邮箱，则原样优先使用
	if email != "" {
		parsed, err := mail.ParseAddress(email)
		if err != nil || parsed.Address != email {
			return "", "", errors.New("发件人邮箱地址格式无效")
		}
		if name == "" {
			name = "Scert"
		}
		return name, email, nil
	}

	username := strings.ToLower(strings.TrimSpace(c.SMTPUsername))
	if c.UsesSmailSystemAccount() {
		account := strings.TrimPrefix(username, "notify:")
		parsed, err := mail.ParseAddress(account)
		parts := strings.Split(account, "@")
		if err != nil || parsed.Address != account || parsed.Name != "" || len(parts) != 2 ||
			strings.ContainsAny(account, "\r\n<>()") {
			return "", "", errors.New("Smail 系统通知用户名必须使用 notify:账号@邮件域名 格式")
		}
		// 用户未单独指定发件人邮箱时，原样使用 notify: 后的账号邮箱地址，发件人名称优先使用用户配置的 SenderName
		if name == "" {
			name = "系统通知"
		}
		return name, account, nil
	}

	parsed, err := mail.ParseAddress(username)
	if err != nil || parsed.Address != username || parsed.Name != "" {
		return "", "", errors.New("请填写 Smail 系统通知用户名；兼容 SMTP 账号必须是完整邮箱地址")
	}
	if name == "" {
		name = "Scert"
	}
	return name, username, nil
}
