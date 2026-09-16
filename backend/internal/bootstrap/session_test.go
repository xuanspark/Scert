package bootstrap

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestBootstrapTokenCanOnlyBeExchangedOnce(t *testing.T) {
	manager, token, err := NewSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Exchange(token, "192.0.2.10:49000")
	if err != nil {
		t.Fatalf("兑换 Bootstrap 一次性令牌失败：%v", err)
	}
	if session.Token == "" || !manager.Authenticate(session.Token) {
		t.Fatal("Bootstrap Cookie 会话未生效")
	}
	if _, err := manager.Exchange(token, "192.0.2.10:49001"); !errors.Is(err, ErrBootstrapTokenInvalid) {
		t.Fatalf("Bootstrap 一次性令牌被重复兑换：%v", err)
	}
	manager.RevokeAll()
	if manager.Authenticate(session.Token) {
		t.Fatal("Bootstrap 会话未被全部注销")
	}
}

func TestBootstrapSessionExpiryAndSourceRateLimit(t *testing.T) {
	manager, _, err := NewSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	manager.now = func() time.Time { return now }
	expiredToken := "expired-session"
	manager.sessions[sha256.Sum256([]byte(expiredToken))] = bootstrapSessionRecord{ExpiresAt: now.Add(-time.Second)}
	if manager.Authenticate(expiredToken) {
		t.Fatal("过期 Bootstrap 会话仍然有效")
	}

	for index := 0; index < bootstrapSourceAttemptLimit; index++ {
		if _, err := manager.Exchange("wrong-token", "198.51.100.20"); !errors.Is(err, ErrBootstrapTokenInvalid) {
			t.Fatalf("错误令牌返回结果不正确：%v", err)
		}
	}
	if _, err := manager.Exchange("wrong-token", "198.51.100.20"); !errors.Is(err, ErrBootstrapRateLimited) {
		t.Fatalf("Bootstrap 来源限流未生效：%v", err)
	}
	manager.now = func() time.Time { return now.Add(bootstrapSourceAttemptWindow + time.Second) }
	if _, err := manager.Exchange("wrong-token", "198.51.100.20"); !errors.Is(err, ErrBootstrapTokenInvalid) {
		t.Fatalf("Bootstrap 来源限流窗口未恢复：%v", err)
	}
}

func TestBootstrapSessionCapacityAndGlobalConcurrencyLimit(t *testing.T) {
	manager, token, err := NewSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	now := manager.now().UTC()
	for index := 0; index < bootstrapSessionCapacity; index++ {
		value := sha256.Sum256([]byte{byte(index)})
		manager.sessions[value] = bootstrapSessionRecord{ExpiresAt: now.Add(time.Minute)}
	}
	if _, err := manager.Exchange(token, "203.0.113.12"); !errors.Is(err, ErrBootstrapSessionFull) {
		t.Fatalf("Bootstrap 会话容量限制未生效：%v", err)
	}

	manager.sessions = make(map[[sha256.Size]byte]bootstrapSessionRecord)
	manager.inFlight = bootstrapExchangeInFlightLimit
	if _, err := manager.Exchange(token, "203.0.113.13"); !errors.Is(err, ErrBootstrapBusy) {
		t.Fatalf("Bootstrap 全局并发限制未生效：%v", err)
	}
}

func TestBootstrapCookieSecurityAttributes(t *testing.T) {
	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	cookie := BootstrapSessionCookie("session-token", expiresAt)
	if cookie.Name != SessionCookieName || cookie.Value != "session-token" {
		t.Fatalf("Bootstrap Cookie 名称或值错误：%+v", cookie)
	}
	if cookie.Path != "/api/v1/bootstrap" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("Bootstrap Cookie 安全属性不完整：%+v", cookie)
	}
	cleared := ClearBootstrapSessionCookie()
	if cleared.MaxAge != -1 || cleared.Value != "" || !cleared.Secure || !cleared.HttpOnly || cleared.SameSite != http.SameSiteStrictMode {
		t.Fatalf("Bootstrap Cookie 清理属性不正确：%+v", cleared)
	}
}
