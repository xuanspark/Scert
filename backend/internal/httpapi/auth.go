package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/auth"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

const (
	authCookieName       = "scert_session"
	authSessionLifetime  = 30 * time.Minute
	rememberSessionLife  = 14 * 24 * time.Hour
	loginWindow          = 15 * time.Minute
	loginBlockDuration   = 5 * time.Minute
	loginFailureLimit    = 5
	authRequestBodyLimit = 16 * 1024
)

type authSession struct {
	UserID            string
	Username          string
	Email             string
	Role              string
	PasswordHash      string
	ExpiresAt         time.Time
	AbsoluteExpiresAt time.Time
}

type loginAttempt struct {
	Failures    int
	WindowStart time.Time
	BlockedTill time.Time
}

type authLoginRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
	Remember   bool   `json:"remember"`
}

func (s *Server) authLogin(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, authRequestBodyLimit)
	var request authLoginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		s.writeError(c, http.StatusBadRequest, "登录请求参数无效", err)
		return
	}

	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	if !cfg.SetupReady {
		s.writeError(c, http.StatusConflict, "请先完成初始化配置", nil)
		return
	}

	key := loginAttemptKey(c.Request, request.Identifier)
	if !s.allowLogin(key) {
		s.writeError(c, http.StatusTooManyRequests, "登录失败次数过多，请稍后再试", nil)
		return
	}

	account, userID, passwordHash, err := s.authenticateAccount(c.Request.Context(), request, cfg)
	if err != nil {
		s.recordLoginFailure(key)
		s.recordAuthAudit(c, "登录失败", "failed", "登录凭据校验失败")
		s.writeError(c, http.StatusUnauthorized, "账号或密码错误", nil)
		return
	}
	s.clearLoginFailures(key)

	token, err := newAuthToken()
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "无法创建登录会话，请稍后重试", err)
		return
	}
	now := s.now()
	absoluteExpiresAt := now.Add(authSessionLifetime)
	if request.Remember {
		absoluteExpiresAt = now.Add(rememberSessionLife)
	}
	expiresAt := minTime(now.Add(authSessionLifetime), absoluteExpiresAt)
	s.authMu.Lock()
	s.authSessions[token] = authSession{
		UserID:            userID,
		Username:          account.Username,
		Email:             account.Email,
		Role:              account.Role,
		PasswordHash:      passwordHash,
		ExpiresAt:         expiresAt,
		AbsoluteExpiresAt: absoluteExpiresAt,
	}
	s.authMu.Unlock()
	s.setAuthCookie(c, token, expiresAt)
	s.recordAuthAudit(c, "登录成功", "success", "用户登录成功")
	c.JSON(http.StatusOK, gin.H{
		"authenticated":  true,
		"account":        account.Username,
		"email":          account.Email,
		"role":           account.Role,
		"is_admin":       account.Role == repository.UserRoleSuperAdmin || account.Role == repository.UserRoleAdmin,
		"next_step":      "complete",
		"required_steps": []string{},
		"expires_at":     expiresAt,
	})
}

func (s *Server) authenticateAccount(ctx context.Context, request authLoginRequest, cfg config.Config) (auth.AdminAccount, string, string, error) {
	account, err := auth.AuthenticateAdmin(request.Identifier, request.Password, cfg.Admin.Username, cfg.Admin.Email, cfg.Admin.PasswordHash)
	if err == nil {
		return account, "primary-super-admin", cfg.Admin.PasswordHash, nil
	}
	if s.users == nil {
		return auth.AdminAccount{}, "", "", auth.ErrInvalidCredentials
	}
	user, lookupErr := s.users.FindByIdentifier(ctx, request.Identifier)
	if lookupErr != nil || user.Status != repository.UserStatusActive {
		return auth.AdminAccount{}, "", "", auth.ErrInvalidCredentials
	}
	account, err = auth.AuthenticateAccount(request.Identifier, request.Password, user.Username, user.Email, user.PasswordHash, user.Role)
	if err != nil {
		return auth.AdminAccount{}, "", "", auth.ErrInvalidCredentials
	}
	return account, user.ID, user.PasswordHash, nil
}

func (s *Server) authSession(c *gin.Context) {
	session, ok := s.currentAuthSession(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"authenticated": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"authenticated":  true,
		"account":        session.Username,
		"email":          session.Email,
		"role":           session.Role,
		"is_admin":       session.Role == repository.UserRoleSuperAdmin || session.Role == repository.UserRoleAdmin,
		"next_step":      "complete",
		"required_steps": []string{},
		"expires_at":     session.ExpiresAt,
	})
}

func (s *Server) authLogout(c *gin.Context) {
	if token, err := c.Cookie(authCookieName); err == nil && token != "" {
		s.authMu.Lock()
		delete(s.authSessions, token)
		s.authMu.Unlock()
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: authCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.requestIsHTTPS(c.Request),
	})
	c.JSON(http.StatusOK, gin.H{"authenticated": false, "message": "已退出登录"})
}

func (s *Server) currentAuthSession(c *gin.Context) (authSession, bool) {
	token, err := c.Cookie(authCookieName)
	if err != nil || strings.TrimSpace(token) == "" {
		return authSession{}, false
	}
	now := s.now()
	s.authMu.RLock()
	session, ok := s.authSessions[token]
	s.authMu.RUnlock()
	if !ok || !session.ExpiresAt.After(now) || (!session.AbsoluteExpiresAt.IsZero() && !session.AbsoluteExpiresAt.After(now)) {
		if ok {
			s.authMu.Lock()
			delete(s.authSessions, token)
			s.authMu.Unlock()
		}
		return authSession{}, false
	}
	if !s.authSessionAccountIsValid(c.Request.Context(), session) {
		s.authMu.Lock()
		delete(s.authSessions, token)
		s.authMu.Unlock()
		s.clearAuthCookie(c)
		return authSession{}, false
	}
	newExpiresAt := minTime(now.Add(authSessionLifetime), session.AbsoluteExpiresAt)
	if newExpiresAt.After(session.ExpiresAt) {
		session.ExpiresAt = newExpiresAt
		s.authMu.Lock()
		if current, exists := s.authSessions[token]; exists {
			current.ExpiresAt = newExpiresAt
			s.authSessions[token] = current
			session = current
		}
		s.authMu.Unlock()
		s.setAuthCookie(c, token, session.ExpiresAt)
	}
	return session, true
}

func (s *Server) authSessionAccountIsValid(ctx context.Context, session authSession) bool {
	if session.UserID == "" {
		return true
	}
	if session.PasswordHash == "" {
		return true
	}
	if session.UserID == "primary-super-admin" {
		s.configMu.RLock()
		cfg := s.config
		s.configMu.RUnlock()
		if !cfg.SetupReady || session.Role != repository.UserRoleSuperAdmin {
			return false
		}
		if !strings.EqualFold(strings.TrimSpace(session.Username), strings.TrimSpace(cfg.Admin.Username)) ||
			!strings.EqualFold(strings.TrimSpace(session.Email), strings.TrimSpace(cfg.Admin.Email)) {
			return false
		}
		return session.PasswordHash == cfg.Admin.PasswordHash
	}
	if s.users == nil {
		return false
	}
	user, err := s.users.GetUserByID(ctx, session.UserID)
	if err != nil || user.Status != repository.UserStatusActive {
		return false
	}
	if user.Role != session.Role || !strings.EqualFold(user.Username, session.Username) || !strings.EqualFold(user.Email, session.Email) {
		return false
	}
	return session.PasswordHash == user.PasswordHash
}

func (s *Server) setAuthCookie(c *gin.Context, token string, expiresAt time.Time) {
	maxAge := int(expiresAt.Sub(s.now()).Seconds())
	http.SetCookie(c.Writer, &http.Cookie{
		Name: authCookieName, Value: token, Path: "/",
		Expires: expiresAt, MaxAge: max(1, maxAge),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.requestIsHTTPS(c.Request),
	})
}

func (s *Server) clearAuthCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: authCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.requestIsHTTPS(c.Request),
	})
}

func (s *Server) invalidateUserSessions(userID string) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	for token, session := range s.authSessions {
		if session.UserID == userID {
			delete(s.authSessions, token)
		}
	}
}

func (s *Server) allowLogin(key string) bool {
	now := s.now()
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	attempt, ok := s.loginAttempts[key]
	if !ok {
		return true
	}
	if !attempt.BlockedTill.IsZero() && attempt.BlockedTill.After(now) {
		return false
	}
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) >= loginWindow {
		delete(s.loginAttempts, key)
	}
	return true
}

func (s *Server) recordLoginFailure(key string) {
	now := s.now()
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	attempt := s.loginAttempts[key]
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) >= loginWindow {
		attempt = loginAttempt{WindowStart: now}
	}
	attempt.Failures++
	if attempt.Failures >= loginFailureLimit {
		attempt.BlockedTill = now.Add(loginBlockDuration)
	}
	s.loginAttempts[key] = attempt
}

func (s *Server) clearLoginFailures(key string) {
	s.loginMu.Lock()
	delete(s.loginAttempts, key)
	s.loginMu.Unlock()
}

func (s *Server) recordAuthAudit(c *gin.Context, action, status, message string) {
	if s.audit == nil {
		return
	}
	if err := s.audit.CreateAuditLog(c.Request.Context(), repository.AuditLog{
		Action: action, ResourceType: "auth", ResourceID: "login", Status: status, Message: message,
	}); err != nil && s.logger != nil {
		s.logger.Warn("保存登录审计日志失败", "error", err)
	}
}

func loginAttemptKey(request *http.Request, identifier string) string {
	remote := "unknown"
	if request != nil {
		remote = request.RemoteAddr
		if host, _, err := net.SplitHostPort(remote); err == nil {
			remote = host
		}
	}
	return strings.ToLower(strings.TrimSpace(remote + "|" + identifier))
}

func newAuthToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func (s *Server) requestIsHTTPS(request *http.Request) bool {
	if request == nil {
		return false
	}
	if request.TLS != nil {
		return true
	}
	s.configMu.RLock()
	proxyMode := s.config.Server.HTTPSMode == "proxy"
	s.configMu.RUnlock()
	if !proxyMode {
		return false
	}
	forwardedProto := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwardedProto, "https")
}

func minTime(left, right time.Time) time.Time {
	if right.IsZero() || left.Before(right) {
		return left
	}
	return right
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
