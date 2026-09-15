package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestLoginPrunesUnvisitedExpiredState(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "admin"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	server := New(Dependencies{Config: cfg})
	now := time.Now()
	server.now = func() time.Time { return now }
	server.authSessions["expired"] = authSession{ExpiresAt: now.Add(-time.Hour)}
	server.authSessions["absolute-expired"] = authSession{ExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(-time.Second)}
	server.loginAttempts["old-key"] = loginAttempt{WindowStart: now.Add(-time.Hour)}
	server.loginAttempts["blocked-key"] = loginAttempt{WindowStart: now.Add(-time.Hour), BlockedTill: now.Add(time.Minute)}
	response := loginAuthTest(t, server, "admin", "AdminPass123", false)
	if response.Code != http.StatusOK {
		t.Fatalf("登录失败：%d", response.Code)
	}
	if len(server.authSessions) != 1 {
		t.Fatal("新登录必须清理不会再被访问的过期会话")
	}
	if _, exists := server.loginAttempts["old-key"]; exists {
		t.Fatal("新登录必须清理过期限流键")
	}
	if _, exists := server.loginAttempts["blocked-key"]; !exists {
		t.Fatal("清理不能缩短仍然有效的封禁期")
	}
}

func TestSessionCapacityDoesNotEvictActiveSessions(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "admin"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	server := New(Dependencies{Config: cfg})
	now := time.Now()
	server.now = func() time.Time { return now }
	for index := 0; index < authStateCapacity; index++ {
		server.authSessions[fmt.Sprint(index)] = authSession{ExpiresAt: now.Add(time.Minute)}
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"identifier":"admin","password":"AdminPass123"}`))
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || len(response.Result().Cookies()) != 0 || len(server.authSessions) != authStateCapacity {
		t.Fatal("会话容量已满时应明确拒绝，不得踢出其他有效会话或设置新 Cookie")
	}
	now = now.Add(2 * time.Minute)
	response = loginAuthTest(t, server, "admin", "AdminPass123", false)
	if response.Code != http.StatusOK || len(server.authSessions) != 1 {
		t.Fatal("过期会话清理后必须恢复登录能力")
	}
}

type pausedSessionRepository struct {
	repository.UserRepository
	started chan struct{}
	release chan struct{}
	user    repository.User
}

func (r *pausedSessionRepository) GetUserByID(ctx context.Context, _ string) (repository.User, error) {
	close(r.started)
	select {
	case <-r.release:
		return r.user, nil
	case <-ctx.Done():
		return repository.User{}, ctx.Err()
	}
}

func TestConcurrentLogoutWinsOverPausedSessionRefresh(t *testing.T) {
	users := &pausedSessionRepository{
		started: make(chan struct{}), release: make(chan struct{}),
		user: repository.User{ID: "operator", Username: "operator", Email: "operator@example.test", PasswordHash: "test-hash", Role: repository.UserRoleUser, Status: repository.UserStatusActive},
	}
	server := New(Dependencies{Config: readyTestConfig(t.TempDir()), Users: users})
	now := time.Now()
	server.authSessions["paused"] = authSession{UserID: users.user.ID, Username: users.user.Username, Email: users.user.Email, Role: users.user.Role, PasswordHash: users.user.PasswordHash, ExpiresAt: now.Add(time.Minute), AbsoluteExpiresAt: now.Add(time.Hour)}
	cookie := &http.Cookie{Name: authCookieName, Value: "paused"}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- authSessionRequest(server, cookie) }()
	select {
	case <-users.started:
	case <-time.After(time.Second):
		close(users.release)
		t.Fatal("会话未进入账号校验阶段")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(cookie)
	server.Handler().ServeHTTP(httptest.NewRecorder(), request)
	close(users.release)
	response := <-done
	if response.Code != http.StatusUnauthorized || len(response.Result().Cookies()) != 0 {
		t.Fatal("并发退出后的旧请求不能继续刷新会话或设置 Cookie")
	}
}

func TestLoginStateHasFiniteCapacity(t *testing.T) {
	server := New(Dependencies{Config: readyTestConfig(t.TempDir())})
	for index := 0; index < 20000; index++ {
		key := fmt.Sprintf("unique-%d", index)
		if server.allowLogin(key) {
			server.recordLoginFailure(key)
		}
	}
	if len(server.loginAttempts) > 4096 {
		t.Fatalf("失败登录记录无界增长：%d", len(server.loginAttempts))
	}
	if server.allowLogin("new-key-after-capacity") {
		t.Fatal("限流表已满时不能继续接受未知登录键")
	}
}

func TestLoginAttemptKeyDoesNotRetainLargeIdentifier(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	identifier := fmt.Sprintf("%016000d", 1)
	if len(loginAttemptKey(request, identifier)) > 128 {
		t.Fatal("限流键不应保留大体积用户输入")
	}
	if loginAttemptKey(request, " ADMIN ") != loginAttemptKey(request, "admin") {
		t.Fatal("账号规范化规则不能因散列键改变")
	}
}
