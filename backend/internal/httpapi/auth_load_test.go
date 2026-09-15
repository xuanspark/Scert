package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIncompleteSessionsFailClosed(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "admin"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	for _, field := range []string{"user_id", "username", "hash", "role", "absolute", "inverted"} {
		t.Run(field, func(t *testing.T) {
			s := New(Dependencies{Config: cfg})
			cookie := loginAuthTest(t, s, "admin", "AdminPass123", false).Result().Cookies()[0]
			session := s.authSessions[cookie.Value]
			switch field {
			case "user_id":
				session.UserID = ""
			case "username":
				session.Username = ""
			case "hash":
				session.PasswordHash = ""
			case "role":
				session.Role = "unknown"
			case "absolute":
				session.AbsoluteExpiresAt = time.Time{}
			case "inverted":
				session.AbsoluteExpiresAt = session.ExpiresAt.Add(-time.Second)
			}
			s.authSessions[cookie.Value] = session
			if response := authSessionRequest(s, cookie); response.Code != http.StatusUnauthorized {
				t.Fatal("不完整会话必须拒绝")
			}
			if _, ok := s.authSessions[cookie.Value]; ok {
				t.Fatal("失效会话必须移除")
			}
		})
	}
}

func TestLoginAdmissionConcurrentBurstAndRecovery(t *testing.T) {
	s := New(Dependencies{Config: readyTestConfig(t.TempDir())})
	const workers = 512
	start, release := make(chan struct{}), make(chan struct{})
	var attempted sync.WaitGroup
	var done sync.WaitGroup
	var accepted atomic.Int32
	attempted.Add(workers)
	done.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer done.Done()
			<-start
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i%250+1)
			key := loginAttemptKey(r, fmt.Sprint(i))
			ok := s.reserveLogin(r, key)
			if ok {
				accepted.Add(1)
			}
			attempted.Done()
			if ok {
				<-release
				s.releaseLogin(r, key)
			}
		}(i)
	}
	close(start)
	attempted.Wait()
	if accepted.Load() != loginConcurrentLimit {
		t.Errorf("并发容量错误：%d", accepted.Load())
	}
	close(release)
	done.Wait()
	if s.loginInFlight != 0 || len(s.loginPending) != 0 {
		t.Fatal("并发预约没有完整释放")
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.RemoteAddr = "198.51.100.1:1234"
	key := loginAttemptKey(r, "normal")
	if !s.reserveLogin(r, key) {
		t.Fatal("突发结束后正常用户无法恢复")
	}
	s.releaseLogin(r, key)
}

func TestRandomIdentifiersCannotExhaustGlobalLoginState(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "admin"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	s := New(Dependencies{Config: cfg})
	now := time.Now()
	s.now = func() time.Time { return now }
	for i := 0; i < 5000; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(fmt.Sprintf(`{"identifier":"unknown-%d","password":"wrong"}`, i)))
		r.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if i >= loginSourceRequestLimit && w.Code != http.StatusTooManyRequests {
			t.Fatalf("随机账号绕过来源限流：%d", w.Code)
		}
	}
	if len(s.loginAttempts) > loginSourceRequestLimit {
		t.Fatal("单一来源挤占了全局失败表")
	}
	normalRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"identifier":"admin","password":"AdminPass123"}`))
	normalRequest.RemoteAddr = "198.51.100.1:1234"
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, normalRequest)
	if response.Code != http.StatusOK {
		t.Fatal("独立来源正常用户不能登录")
	}
	now = now.Add(2 * time.Minute)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"identifier":"admin","password":"AdminPass123"}`))
	r.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("来源窗口结束后无法恢复：%d", w.Code)
	}
}

func TestLoginAttemptTableSaturationRecoversAfterWindow(t *testing.T) {
	s := New(Dependencies{Config: readyTestConfig(t.TempDir())})
	now := time.Now()
	s.now = func() time.Time { return now }
	for i := 0; i < authStateCapacity; i++ {
		s.recordLoginFailure(fmt.Sprint(i))
	}
	if s.allowLogin("normal") {
		t.Fatal("满容量不能无界增加新键")
	}
	now = now.Add(loginWindow + time.Second)
	if !s.allowLogin("normal") || len(s.loginAttempts) != 0 {
		t.Fatal("窗口到期后未恢复")
	}
}

func TestLoginBlockExpiresAndUserSessionQuotaRecovers(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "admin"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	s := New(Dependencies{Config: cfg})
	now := time.Now()
	s.now = func() time.Time { return now }
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	key := loginAttemptKey(r, "admin")
	for i := 0; i < loginFailureLimit; i++ {
		s.recordLoginFailure(key)
	}
	if s.allowLogin(key) {
		t.Fatal("封禁期间不能登录")
	}
	now = now.Add(loginBlockDuration + time.Second)
	if !s.allowLogin(key) || !s.reserveLogin(r, key) {
		t.Fatal("封禁到期后必须恢复预约")
	}
	s.releaseLogin(r, key)
	var first *http.Cookie
	for i := 0; i < authUserSessionCapacity; i++ {
		cookie := loginAuthTest(t, s, "admin", "AdminPass123", false).Result().Cookies()[0]
		if first == nil {
			first = cookie
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"identifier":"admin","password":"AdminPass123"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, request)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("单账号不能占满全局会话容量")
	}
	logout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.AddCookie(first)
	s.Handler().ServeHTTP(httptest.NewRecorder(), logout)
	if loginAuthTest(t, s, "admin", "AdminPass123", false).Code != http.StatusOK {
		t.Fatal("主动退出后不能恢复登录")
	}
}
