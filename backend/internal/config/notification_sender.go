package config

import (
	"errors"
	"net/mail"
	"strings"
)

// SenderIdentity 根据系统通知账号确定发件人，不能将管理员邮箱当作 SMTP 发件人。
func (c NotificationConfig) SenderIdentity() (string, string, error) {
	name := strings.TrimSpace(c.SenderName)
	email := strings.TrimSpace(c.SenderEmail)
	if !c.SMTPEnabled {
		return name, email, nil
	}
	username := strings.ToLower(strings.TrimSpace(c.SMTPUsername))
	if strings.HasPrefix(username, "notify:") {
		account := strings.TrimPrefix(username, "notify:")
		parsed, err := mail.ParseAddress(account)
		parts := strings.Split(account, "@")
		if err != nil || parsed.Address != account || parsed.Name != "" || len(parts) != 2 ||
			strings.ContainsAny(account, "\r\n<>()") {
			return "", "", errors.New("Smail 系统通知用户名必须使用 notify:账号@邮件域名 格式")
		}
		// Smail 保留该发件地址；不能使用登录账号、管理员邮箱或自定义名称替代。
		return "系统通知", "notification+system@" + parts[1], nil
	}
	if email == "" {
		parsed, err := mail.ParseAddress(username)
		if err != nil || parsed.Address != username || parsed.Name != "" {
			return "", "", errors.New("请填写 Smail 系统通知用户名；兼容 SMTP 账号必须是完整邮箱地址")
		}
		email = username
	}
	return name, email, nil
}
