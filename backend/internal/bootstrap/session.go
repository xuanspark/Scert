package bootstrap

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	SessionCookieName              = "scert_bootstrap_session"
	bootstrapTokenLifetime         = 30 * time.Minute
	bootstrapSessionLifetime       = 30 * time.Minute
	bootstrapSessionCapacity       = 32
	bootstrapExchangeInFlightLimit = 8
	bootstrapSourceCapacity        = 256
	bootstrapSourceAttemptLimit    = 6
	bootstrapSourceAttemptWindow   = time.Minute
	bootstrapTokenSize             = 32
	bootstrapSessionTokenSize      = 32
)

var readRandom = rand.Read

var (
	ErrBootstrapTokenInvalid = errors.New("Bootstrap 一次性令牌无效或已经使用")
	ErrBootstrapTokenExpired = errors.New("Bootstrap 一次性令牌已经过期，请重启服务生成新地址")
	ErrBootstrapRateLimited  = errors.New("Bootstrap 验证尝试过多，请稍后重试")
	ErrBootstrapBusy         = errors.New("Bootstrap 验证请求过多，请稍后重试")
	ErrBootstrapSessionFull  = errors.New("Bootstrap 会话数量已达上限，请等待现有会话过期后重试")
)

type BootstrapSession struct {
	Token     string
	ExpiresAt time.Time
}

type bootstrapSessionRecord struct {
	ExpiresAt time.Time
}

type bootstrapSourceAttempts struct {
	WindowStarted time.Time
	Count         int
}

type SessionManager struct {
	mu          sync.Mutex
	now         func() time.Time
	tokenHash   [sha256.Size]byte
	tokenExpiry time.Time
	tokenUsed   bool
	sessions    map[[sha256.Size]byte]bootstrapSessionRecord
	sources     map[string]bootstrapSourceAttempts
	inFlight    int
}

func NewSessionManager() (*SessionManager, string, error) {
	token, err := secureRandomToken(bootstrapTokenSize)
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	return &SessionManager{
		now:         time.Now,
		tokenHash:   sha256.Sum256([]byte(token)),
		tokenExpiry: now.Add(bootstrapTokenLifetime),
		sessions:    make(map[[sha256.Size]byte]bootstrapSessionRecord),
		sources:     make(map[string]bootstrapSourceAttempts),
	}, token, nil
}

func (m *SessionManager) Exchange(token, source string) (BootstrapSession, error) {
	if m == nil {
		return BootstrapSession{}, errors.New("Bootstrap 会话服务尚未初始化")
	}
	source = normalizeBootstrapSource(source)
	if source == "" {
		return BootstrapSession{}, errors.New("Bootstrap 请求来源无效")
	}
	now := m.now().UTC()
	if err := m.reserveExchange(source, now); err != nil {
		return BootstrapSession{}, err
	}
	defer m.releaseExchange()

	candidateHash := sha256.Sum256([]byte(strings.TrimSpace(token)))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(now)
	if m.tokenUsed {
		return BootstrapSession{}, ErrBootstrapTokenInvalid
	}
	if !m.tokenExpiry.After(now) {
		return BootstrapSession{}, ErrBootstrapTokenExpired
	}
	if subtle.ConstantTimeCompare(candidateHash[:], m.tokenHash[:]) != 1 {
		return BootstrapSession{}, ErrBootstrapTokenInvalid
	}
	if len(m.sessions) >= bootstrapSessionCapacity {
		return BootstrapSession{}, ErrBootstrapSessionFull
	}
	sessionToken, err := secureRandomToken(bootstrapSessionTokenSize)
	if err != nil {
		return BootstrapSession{}, err
	}
	expiresAt := now.Add(bootstrapSessionLifetime)
	m.sessions[sha256.Sum256([]byte(sessionToken))] = bootstrapSessionRecord{ExpiresAt: expiresAt}
	m.tokenUsed = true
	return BootstrapSession{Token: sessionToken, ExpiresAt: expiresAt}, nil
}

func (m *SessionManager) Authenticate(token string) bool {
	if m == nil || strings.TrimSpace(token) == "" {
		return false
	}
	now := m.now().UTC()
	hash := sha256.Sum256([]byte(token))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(now)
	record, ok := m.sessions[hash]
	return ok && record.ExpiresAt.After(now)
}

func (m *SessionManager) RevokeAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	clear(m.sessions)
	m.mu.Unlock()
}

func (m *SessionManager) reserveExchange(source string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(now)
	if m.inFlight >= bootstrapExchangeInFlightLimit {
		return ErrBootstrapBusy
	}
	attempts, ok := m.sources[source]
	if !ok && len(m.sources) >= bootstrapSourceCapacity {
		return ErrBootstrapRateLimited
	}
	if !ok || now.Sub(attempts.WindowStarted) >= bootstrapSourceAttemptWindow {
		attempts = bootstrapSourceAttempts{WindowStarted: now}
	}
	if attempts.Count >= bootstrapSourceAttemptLimit {
		return ErrBootstrapRateLimited
	}
	attempts.Count++
	m.sources[source] = attempts
	m.inFlight++
	return nil
}

func (m *SessionManager) releaseExchange() {
	m.mu.Lock()
	if m.inFlight > 0 {
		m.inFlight--
	}
	m.mu.Unlock()
}

func (m *SessionManager) pruneLocked(now time.Time) {
	for hash, record := range m.sessions {
		if !record.ExpiresAt.After(now) {
			delete(m.sessions, hash)
		}
	}
	for source, attempts := range m.sources {
		if now.Sub(attempts.WindowStarted) >= bootstrapSourceAttemptWindow {
			delete(m.sources, source)
		}
	}
}

func BootstrapSessionCookie(token string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/api/v1/bootstrap",
		Expires:  expiresAt.UTC(),
		MaxAge:   max(1, int(time.Until(expiresAt).Seconds())),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

func ClearBootstrapSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/api/v1/bootstrap",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

func normalizeBootstrapSource(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	ip := net.ParseIP(strings.Trim(value, "[]"))
	if ip == nil || ip.IsUnspecified() {
		return ""
	}
	return ip.String()
}

func secureRandomToken(size int) (string, error) {
	if size < 16 {
		return "", errors.New("安全令牌长度不足")
	}
	buffer := make([]byte, size)
	if _, err := readRandom(buffer); err != nil {
		return "", errors.New("生成安全令牌失败")
	}
	return encodeToken(buffer), nil
}
