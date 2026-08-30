package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestAuthLoginSupportsUserNameEmailAndDisabledAccounts(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	cfg.Database.DSN = filepath.Join(directory, "data", "auth-api.db")
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatalf("打开认证测试数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatalf("迁移认证测试数据库失败：%v", err)
	}
	users := repository.NewSQLUserRepository(db.DB, "sqlite")
	if err := users.EnsureSuperAdmin(t.Context(), repository.User{
		ID: "primary-super-admin", Username: cfg.Admin.Username, Email: cfg.Admin.Email, PasswordHash: cfg.Admin.PasswordHash,
	}); err != nil {
		t.Fatalf("保存测试超级管理员失败：%v", err)
	}
	if err := users.CreateUser(t.Context(), repository.User{
		ID: "operator-1", Username: "operator", Email: "operator@example.test",
		PasswordHash: httpUserPasswordHash(t, "Operator123"), Role: repository.UserRoleAdmin, Status: repository.UserStatusActive,
	}); err != nil {
		t.Fatalf("保存测试管理员失败：%v", err)
	}
	if err := users.CreateUser(t.Context(), repository.User{
		ID: "disabled-1", Username: "disabled", Email: "disabled@example.test",
		PasswordHash: httpUserPasswordHash(t, "Disabled123"), Role: repository.UserRoleUser, Status: repository.UserStatusDisabled,
	}); err != nil {
		t.Fatalf("保存测试禁用用户失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, Users: users})

	for _, identifier := range []string{"operator", "OPERATOR@EXAMPLE.TEST"} {
		response := loginAuthTest(t, server, identifier, "Operator123", false)
		var payload struct {
			Account string `json:"account"`
			Email   string `json:"email"`
			Role    string `json:"role"`
			IsAdmin bool   `json:"is_admin"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatalf("解析用户登录响应失败：%v", err)
		}
		if payload.Account != "operator" || payload.Email != "operator@example.test" || payload.Role != repository.UserRoleAdmin || !payload.IsAdmin {
			t.Fatalf("用户登录响应内容错误：%+v", payload)
		}
	}

	disabled := loginAuthTest(t, server, "disabled", "Disabled123", false)
	if disabled.Code != http.StatusUnauthorized {
		t.Fatalf("禁用用户登录应返回 401，实际为 %d：%s", disabled.Code, disabled.Body.String())
	}
}

func TestAuthSessionSlidesAndHonorsAbsoluteRememberExpiration(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}})
	current := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	server.now = func() time.Time { return current }

	login := loginAuthTest(t, server, "admin", "AdminPass123", true)
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("记住登录响应未返回会话 Cookie")
	}
	token := cookies[0].Value
	server.authMu.RLock()
	initial := server.authSessions[token]
	server.authMu.RUnlock()
	if !initial.AbsoluteExpiresAt.Equal(current.Add(rememberSessionLife)) || !initial.ExpiresAt.Equal(current.Add(authSessionLifetime)) {
		t.Fatalf("记住登录会话期限错误：%+v", initial)
	}

	current = current.Add(20 * time.Minute)
	sessionResponse := httptest.NewRecorder()
	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	sessionRequest.AddCookie(cookies[0])
	server.Handler().ServeHTTP(sessionResponse, sessionRequest)
	if sessionResponse.Code != http.StatusOK {
		t.Fatalf("有效会话检查失败：%d %s", sessionResponse.Code, sessionResponse.Body.String())
	}
	server.authMu.RLock()
	sliding := server.authSessions[token]
	server.authMu.RUnlock()
	if !sliding.ExpiresAt.Equal(current.Add(authSessionLifetime)) {
		t.Fatalf("会话未按访问时间滑动续期：%+v", sliding)
	}

	current = current.Add(rememberSessionLife)
	expired := httptest.NewRecorder()
	expiredRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	expiredRequest.AddCookie(cookies[0])
	server.Handler().ServeHTTP(expired, expiredRequest)
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("超过绝对期限的会话应返回 401，实际为 %d：%s", expired.Code, expired.Body.String())
	}
}

func TestAuthSessionIsInvalidatedWhenUserChanges(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	cfg.Database.DSN = filepath.Join(directory, "data", "auth-invalidation.db")
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatalf("打开会话失效测试数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatalf("迁移会话失效测试数据库失败：%v", err)
	}
	users := repository.NewSQLUserRepository(db.DB, "sqlite")
	if err := users.EnsureSuperAdmin(t.Context(), repository.User{
		ID: "primary-super-admin", Username: cfg.Admin.Username, Email: cfg.Admin.Email, PasswordHash: cfg.Admin.PasswordHash,
	}); err != nil {
		t.Fatalf("保存会话失效测试超级管理员失败：%v", err)
	}
	if err := users.CreateUser(t.Context(), repository.User{
		ID: "operator-1", Username: "operator", Email: "operator@example.test",
		PasswordHash: httpUserPasswordHash(t, "Operator123"), Role: repository.UserRoleAdmin, Status: repository.UserStatusActive,
	}); err != nil {
		t.Fatalf("保存会话失效测试用户失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, Users: users})

	login := loginAuthTest(t, server, "operator", "Operator123", false)
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("会话失效测试未返回 Cookie")
	}
	if err := users.UpdateUser(t.Context(), repository.User{
		ID: "operator-1", Username: "operator", Email: "operator@example.test",
		PasswordHash: httpUserPasswordHash(t, "Operator123"), Role: repository.UserRoleAdmin, Status: repository.UserStatusDisabled,
	}); err != nil {
		t.Fatalf("禁用会话失效测试用户失败：%v", err)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(cookies[0])
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("禁用用户的旧会话应返回 401，实际为 %d：%s", response.Code, response.Body.String())
	}
	server.authMu.RLock()
	_, exists := server.authSessions[cookies[0].Value]
	server.authMu.RUnlock()
	if exists {
		t.Fatal("禁用用户的旧会话未从内存中清理")
	}
}

func loginAuthTest(t *testing.T, server *Server, identifier, password string, remember bool) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"identifier":"` + identifier + `","password":"` + password + `","remember":` + boolString(remember) + `}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if remember && response.Code != http.StatusOK {
		t.Fatalf("记住登录失败：%d %s", response.Code, response.Body.String())
	}
	if !remember && identifier != "disabled" && response.Code != http.StatusOK {
		t.Fatalf("登录失败：%s，状态码=%d，响应=%s", identifier, response.Code, response.Body.String())
	}
	return response
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestAuthSessionIsInvalidatedWhenUserPasswordChangesOrIsDeleted(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	cfg.Database.DSN = filepath.Join(directory, "data", "auth-invalidation-actions.db")
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatalf("打开用户会话操作测试数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatalf("迁移用户会话操作测试数据库失败：%v", err)
	}
	users := repository.NewSQLUserRepository(db.DB, "sqlite")
	if err := users.EnsureSuperAdmin(t.Context(), repository.User{
		ID: "primary-super-admin", Username: cfg.Admin.Username, Email: cfg.Admin.Email, PasswordHash: cfg.Admin.PasswordHash,
	}); err != nil {
		t.Fatalf("保存用户会话操作测试超级管理员失败：%v", err)
	}
	if err := users.CreateUser(t.Context(), repository.User{
		ID: "operator-1", Username: "operator", Email: "operator@example.test",
		PasswordHash: httpUserPasswordHash(t, "Operator123"), Role: repository.UserRoleAdmin, Status: repository.UserStatusActive,
	}); err != nil {
		t.Fatalf("保存用户会话操作测试用户失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, Users: users})

	passwordLogin := loginAuthTest(t, server, "operator", "Operator123", false)
	passwordCookie := passwordLogin.Result().Cookies()[0]
	if err := users.UpdateUser(t.Context(), repository.User{
		ID: "operator-1", Username: "operator", Email: "operator@example.test",
		PasswordHash: httpUserPasswordHash(t, "Changed123"), Role: repository.UserRoleAdmin, Status: repository.UserStatusActive,
	}); err != nil {
		t.Fatalf("修改用户密码失败：%v", err)
	}
	passwordResponse := authSessionRequest(server, passwordCookie)
	if passwordResponse.Code != http.StatusUnauthorized {
		t.Fatalf("用户改密后旧会话应返回 401，实际为 %d：%s", passwordResponse.Code, passwordResponse.Body.String())
	}

	deleteLogin := loginAuthTest(t, server, "operator", "Changed123", false)
	deleteCookie := deleteLogin.Result().Cookies()[0]
	if err := users.DeleteUser(t.Context(), "operator-1"); err != nil {
		t.Fatalf("删除用户失败：%v", err)
	}
	deleteResponse := authSessionRequest(server, deleteCookie)
	if deleteResponse.Code != http.StatusUnauthorized {
		t.Fatalf("删除用户后旧会话应返回 401，实际为 %d：%s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestPrimarySuperAdminSessionIsInvalidatedWhenConfigurationPasswordChanges(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}})

	login := loginAuthTest(t, server, "admin", "AdminPass123", false)
	cookie := login.Result().Cookies()[0]
	server.configMu.Lock()
	server.config.Admin.PasswordHash = httpUserPasswordHash(t, "Changed123")
	server.configMu.Unlock()

	response := authSessionRequest(server, cookie)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("主超级管理员改密后旧会话应返回 401，实际为 %d：%s", response.Code, response.Body.String())
	}
}

func authSessionRequest(server *Server, cookie *http.Cookie) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(cookie)
	server.Handler().ServeHTTP(response, request)
	return response
}
