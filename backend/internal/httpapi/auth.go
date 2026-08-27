package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/auth"
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
	Username  string
	Email     string
	Role      string
	ExpiresAt time.Time
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
		s.writeError(c, http.StatusBadRequest, "\u767b\u5f55\u8bf7\u6c42\u53c2\u6570\u65e0\u6548", err)
		return
	}

	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	if !cfg.SetupReady {
		s.writeError(c, http.StatusConflict, "\u8bf7\u5148\u5b8c\u6210\u521d\u59cb\u5316\u914d\u7f6e", nil)
		return
	}

	key := loginAttemptKey(c.Request, request.Identifier)
	if !s.allowLogin(key) {
		s.writeError(c, http.StatusTooManyRequests, "\u767b\u5f55\u5931\u8d25\u6b21\u6570\u8fc7\u591a\uff0c\u8bf7\u7a0d\u540e\u518d\u8bd5", nil)
		return
	}
	account, err := auth.AuthenticateAdmin(
		request.Identifier,
		request.Password,
		cfg.Admin.Username,
		cfg.Admin.Email,
		cfg.Admin.PasswordHash,
	)
	if err != nil {
		s.recordLoginFailure(key)
		s.recordAuthAudit(c, "\u767b\u5f55\u5931\u8d25", "failed", "\u767b\u5f55\u51ed\u636e\u6821\u9a8c\u5931\u8d25")
		s.writeError(c, http.StatusUnauthorized, "\u8d26\u53f7\u6216\u5bc6\u7801\u9519\u8bef", nil)
		return
	}
	s.clearLoginFailures(key)

	token, err := newAuthToken()
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "\u65e0\u6cd5\u521b\u5efa\u767b\u5f55\u4f1a\u8bdd\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5", err)
		return
	}
	lifetime := authSessionLifetime
	if request.Remember {
		lifetime = rememberSessionLife
	}
	expiresAt := s.now().Add(lifetime)
	s.authMu.Lock()
	s.authSessions[token] = authSession{
		Username:  account.Username,
		Email:     account.Email,
		Role:      account.Role,
		ExpiresAt: expiresAt,
	}
	s.authMu.Unlock()
	s.setAuthCookie(c, token, expiresAt)
	s.recordAuthAudit(c, "\u767b\u5f55\u6210\u529f", "success", "\u8d85\u7ea7\u7ba1\u7406\u5458\u767b\u5f55\u6210\u529f")
	c.JSON(http.StatusOK, gin.H{
		"authenticated":  true,
		"account":        account.Username,
		"email":          account.Email,
		"role":           account.Role,
		"is_admin":       true,
		"next_step":      "complete",
		"required_steps": []string{},
		"expires_at":     expiresAt,
	})
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
		"is_admin":       session.Role == "super_admin" || session.Role == "admin",
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
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: requestIsHTTPS(c.Request),
	})
	c.JSON(http.StatusOK, gin.H{"authenticated": false, "message": "\u5df2\u9000\u51fa\u767b\u5f55"})
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
	if !ok {
		return authSession{}, false
	}
	if !session.ExpiresAt.After(now) {
		s.authMu.Lock()
		delete(s.authSessions, token)
		s.authMu.Unlock()
		return authSession{}, false
	}
	return session, true
}

func (s *Server) setAuthCookie(c *gin.Context, token string, expiresAt time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: authCookieName, Value: token, Path: "/",
		Expires: expiresAt, MaxAge: max(1, int(time.Until(expiresAt).Seconds())),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: requestIsHTTPS(c.Request),
	})
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
		s.logger.Warn("\u4fdd\u5b58\u767b\u5f55\u5ba1\u8ba1\u65e5\u5fd7\u5931\u8d25", "error", err)
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

func requestIsHTTPS(request *http.Request) bool {
	if request == nil {
		return false
	}
	return request.TLS != nil || strings.EqualFold(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")), "https")
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
