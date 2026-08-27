package auth

import (
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const dummyPasswordHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

type AdminAccount struct {
	Username string
	Email    string
	Role     string
}

// AuthenticateAdmin 统一执行用户名或邮箱登录，并只返回后端确认的真实权限。
func AuthenticateAdmin(identifier, password, username, email, passwordHash string) (AdminAccount, error) {
	matched := IsMatch(identifier, username, email)
	if !matched || strings.TrimSpace(password) == "" || len([]byte(password)) > 256 {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(password))
		return AdminAccount{}, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return AdminAccount{}, ErrInvalidCredentials
	}
	return AdminAccount{
		Username: strings.TrimSpace(username),
		Email:    strings.TrimSpace(email),
		Role:     "super_admin",
	}, nil
}

func IsInvalidCredentials(err error) bool {
	return errors.Is(err, ErrInvalidCredentials)
}
