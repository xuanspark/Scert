package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestChangePasswordUpdatesPrimarySuperAdminAndInvalidatesSession(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "scert.example.test"
	configPath := filepath.Join(directory, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存改密测试配置失败：%v", err)
	}
	t.Setenv("SCERT_WEB_ENABLED", "false")
	server := New(Dependencies{Config: cfg, ConfigPath: configPath, Repository: &stubDomainRepository{}})
	login := loginAuthTest(t, server, "admin", "AdminPass123", false)
	cookie := login.Result().Cookies()[0]

	response := performChangePasswordRequest(t, server, cookie, `{"current_password":"AdminPass123","new_password":"NewAdmin123","confirm_password":"NewAdmin123"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("超级管理员改密失败：%d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Authenticated bool `json:"authenticated"`
		RequiresLogin bool `json:"requires_login"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析改密响应失败：%v", err)
	}
	if payload.Authenticated || !payload.RequiresLogin {
		t.Fatalf("改密响应未要求重新登录：%+v", payload)
	}

	oldSession := authSessionRequest(server, cookie)
	if oldSession.Code != http.StatusUnauthorized {
		t.Fatalf("改密后旧会话应失效，实际为 %d：%s", oldSession.Code, oldSession.Body.String())
	}
	oldLogin := performLoginRequest(t, server, "admin", "AdminPass123")
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("改密后旧密码不应登录成功，实际为 %d", oldLogin.Code)
	}
	newLogin := loginAuthTest(t, server, "admin", "NewAdmin123", false)
	if newLogin.Code != http.StatusOK {
		t.Fatalf("改密后新密码登录失败：%d %s", newLogin.Code, newLogin.Body.String())
	}

	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("读取改密后的配置失败：%v", err)
	}
	if saved.Admin.PasswordHash == cfg.Admin.PasswordHash || saved.Admin.PasswordHash == "" {
		t.Fatal("改密后的配置没有保存新的密码摘要")
	}
	if saved.Web.Enabled != cfg.Web.Enabled {
		t.Fatal("修改密码不应把环境变量覆盖值写回配置文件")
	}
}

func TestChangePasswordUpdatesDatabaseUser(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Database.DSN = filepath.Join(directory, "data", "change-password.db")
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatalf("打开数据库改密测试数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatalf("迁移数据库改密测试数据库失败：%v", err)
	}
	users := repository.NewSQLUserRepository(db.DB, "sqlite")
	if err := users.CreateUser(t.Context(), repository.User{
		ID: "operator-1", Username: "operator", Email: "operator@example.test",
		PasswordHash: httpUserPasswordHash(t, "Operator123"), Role: repository.UserRoleAdmin, Status: repository.UserStatusActive,
	}); err != nil {
		t.Fatalf("创建数据库改密测试用户失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, Users: users})
	login := loginAuthTest(t, server, "operator", "Operator123", false)
	if login.Code != http.StatusOK {
		t.Fatalf("数据库用户登录失败：%d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	response := performChangePasswordRequest(t, server, cookie, `{"current_password":"Operator123","new_password":"NewOper123","confirm_password":"NewOper123"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("数据库用户改密失败：%d %s", response.Code, response.Body.String())
	}
	if oldSession := authSessionRequest(server, cookie); oldSession.Code != http.StatusUnauthorized {
		t.Fatalf("数据库用户改密后旧会话应失效，实际为 %d", oldSession.Code)
	}
	if newLogin := loginAuthTest(t, server, "operator@example.test", "NewOper123", false); newLogin.Code != http.StatusOK {
		t.Fatalf("数据库用户改密后新密码登录失败：%d %s", newLogin.Code, newLogin.Body.String())
	}
}

func TestChangePasswordRejectsInvalidInput(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "scert.example.test"
	cfgPath := filepath.Join(directory, "config", "scert.json")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("保存非法改密测试配置失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, ConfigPath: cfgPath, Repository: &stubDomainRepository{}})
	login := loginAuthTest(t, server, "admin", "AdminPass123", false)
	cookie := login.Result().Cookies()[0]
	response := performChangePasswordRequest(t, server, cookie, `{"current_password":"wrong-password","new_password":"short","confirm_password":"different"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("非法改密请求应先返回参数错误，实际为 %d：%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "两次输入的新密码不一致") {
		t.Fatalf("非法改密响应未说明确认密码错误：%s", response.Body.String())
	}
}

func performChangePasswordRequest(t *testing.T, server *Server, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func performLoginRequest(t *testing.T, server *Server, identifier, password string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"identifier":"` + identifier + `","password":"` + password + `","remember":false}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}
