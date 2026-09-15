package httpapi

import (
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

const authStateCapacity = 4096
const authUserSessionCapacity = 16

func (session authSession) complete() bool {
	return strings.TrimSpace(session.UserID) != "" && strings.TrimSpace(session.Username) != "" && strings.TrimSpace(session.Email) != "" &&
		strings.TrimSpace(session.PasswordHash) != "" &&
		(session.Role == repository.UserRoleUser || session.Role == repository.UserRoleAdmin || session.Role == repository.UserRoleSuperAdmin)
}

func (session authSession) usableAt(now time.Time) bool {
	return session.complete() && session.ExpiresAt.After(now) && session.AbsoluteExpiresAt.After(now) &&
		!session.ExpiresAt.After(session.AbsoluteExpiresAt)
}

// 调用方持有认证锁；会话表有容量上限，不淘汰仍然有效的用户会话。
func (s *Server) pruneAuthSessionsLocked(now time.Time) {
	for token, session := range s.authSessions {
		if !session.usableAt(now) {
			delete(s.authSessions, token)
		}
	}
}

// 调用方持有限流锁；定期回收且保留有效封禁，避免随机账号使状态无限增长。
func (s *Server) pruneLoginAttemptsLocked(now time.Time) {
	if !s.loginPrunedAt.IsZero() && !now.Before(s.loginPrunedAt) && now.Sub(s.loginPrunedAt) < time.Minute {
		return
	}
	for key, attempt := range s.loginAttempts {
		if !attempt.BlockedTill.After(now) && (attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) >= loginWindow) {
			delete(s.loginAttempts, key)
		}
	}
	s.loginPrunedAt = now
}
