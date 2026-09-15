package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type blockedLoginRepository struct {
	repository.UserRepository
	started chan struct{}
	release chan struct{}
}

func (r *blockedLoginRepository) FindByIdentifier(ctx context.Context, _ string) (repository.User, error) {
	r.started <- struct{}{}
	select {
	case <-r.release:
		return repository.User{}, fmt.Errorf("测试账号不存在")
	case <-ctx.Done():
		return repository.User{}, ctx.Err()
	}
}

func TestHTTPLoginBurstRejectsBeforeAccountLookup(t *testing.T) {
	users := &blockedLoginRepository{started: make(chan struct{}, 128), release: make(chan struct{})}
	s := New(Dependencies{Config: readyTestConfig(t.TempDir()), Users: users})
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	send := func(index int) int {
		body := fmt.Sprintf(`{"identifier":"unknown-%d","password":"wrong"}`, index)
		r, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		// 不信任任意转发头；所有套接字仍来自同一个真实来源。
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", index%250+1))
		response, err := client.Do(r)
		if err != nil {
			t.Errorf("真实 HTTP 请求失败：%v", err)
			return 0
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode
	}
	var pending sync.WaitGroup
	for i := 0; i < loginSourceConcurrentLimit; i++ {
		pending.Add(1)
		go func(i int) { defer pending.Done(); send(i) }(i)
	}
	defer func() { close(users.release); pending.Wait() }()
	for i := 0; i < loginSourceConcurrentLimit; i++ {
		select {
		case <-users.started:
		case <-time.After(5 * time.Second):
			t.Fatal("请求没有到达账号查询阶段")
		}
	}
	var burst sync.WaitGroup
	for i := 0; i < 128; i++ {
		burst.Add(1)
		go func(i int) {
			defer burst.Done()
			if code := send(i + 2); code != http.StatusTooManyRequests {
				t.Errorf("来源并发已满时必须在查询前返回 429：%d", code)
			}
		}(i)
	}
	burst.Wait()
	if len(users.started) != 0 {
		t.Fatal("伪造转发头绕过了并发准入")
	}
}

func TestSessionCannotReviveAfterSlowAccountLookup(t *testing.T) {
	users := &pausedSessionRepository{
		started: make(chan struct{}), release: make(chan struct{}),
		user: repository.User{ID: "operator", Username: "operator", PasswordHash: "test-hash", Role: repository.UserRoleUser, Status: repository.UserStatusActive},
	}
	s := New(Dependencies{Config: readyTestConfig(t.TempDir()), Users: users})
	now := time.Now()
	s.now = func() time.Time { return now }
	s.authSessions["slow"] = authSession{
		UserID: users.user.ID, Username: users.user.Username, PasswordHash: users.user.PasswordHash, Role: users.user.Role,
		ExpiresAt: now.Add(time.Minute), AbsoluteExpiresAt: now.Add(time.Hour),
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- authSessionRequest(s, &http.Cookie{Name: authCookieName, Value: "slow"}) }()
	select {
	case <-users.started:
	case <-time.After(time.Second):
		close(users.release)
		t.Fatal("会话没有进入账号查询阶段")
	}
	now = now.Add(2 * time.Minute)
	close(users.release)
	if response := <-done; response.Code != http.StatusUnauthorized {
		t.Fatal("慢查询不能复活已过期会话")
	}
	if _, exists := s.authSessions["slow"]; exists {
		t.Fatal("过期会话未清理")
	}
}

func TestLoginSourceCapacityAndPendingFailureBounds(t *testing.T) {
	s := New(Dependencies{Config: readyTestConfig(t.TempDir())})
	now := time.Now()
	s.now = func() time.Time { return now }
	for i := 0; i < authStateCapacity; i++ {
		s.loginSources[fmt.Sprint(i)] = loginSource{WindowStart: now, Requests: 1}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	key := loginAttemptKey(r, "admin")
	if s.reserveLogin(r, key) || len(s.loginSources) != authStateCapacity {
		t.Fatal("来源表饱和时必须有界拒绝")
	}
	now = now.Add(time.Minute)
	if !s.reserveLogin(r, key) {
		t.Fatal("来源表过期回收后应恢复")
	}
	s.releaseLogin(r, key)
	for i := 0; i < loginFailureLimit-1; i++ {
		s.recordLoginFailure(key)
	}
	if !s.reserveLogin(r, key) {
		t.Fatal("最后一次尝试应允许进入")
	}
	if s.reserveLogin(r, key) {
		t.Fatal("在途密码计算必须计入剩余失败额度")
	}
	s.releaseLogin(r, key)
}
