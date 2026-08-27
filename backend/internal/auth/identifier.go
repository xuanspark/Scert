package auth

import (
	"errors"
	"net/mail"
	"strings"
)

var ErrInvalidIdentifier = errors.New("登录标识无效")
var ErrInvalidCredentials = errors.New("账号或密码错误")

// NormalizeIdentifier 将用户名或邮箱统一为可比较的登录标识。
func NormalizeIdentifier(identifier string) (string, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" || strings.ContainsAny(identifier, "\r\n") {
		return "", ErrInvalidIdentifier
	}
	if !strings.Contains(identifier, "@") {
		return strings.ToLower(identifier), nil
	}
	identifier = strings.ToLower(identifier)
	parsed, err := mail.ParseAddress(identifier)
	if err != nil || parsed.Address != identifier || parsed.Name != "" {
		return "", ErrInvalidIdentifier
	}
	parts := strings.Split(identifier, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ErrInvalidIdentifier
	}
	return identifier, nil
}

// IsMatch 判断登录标识是否匹配指定的用户名或绑定邮箱。
func IsMatch(identifier, username, email string) bool {
	normalized, err := NormalizeIdentifier(identifier)
	if err != nil {
		return false
	}
	username = strings.ToLower(strings.TrimSpace(username))
	email = strings.ToLower(strings.TrimSpace(email))
	return normalized == username || (email != "" && normalized == email)
}
