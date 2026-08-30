package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestSettingsAPIRequiresAuthentication(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	server := New(Dependencies{Config: cfg})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问设置接口应返回 401，实际为 %d：%s", response.Code, response.Body.String())
	}
}

func TestSettingsAPIUpdatesSafeFieldsWithoutReturningSecrets(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	cfg.Database.DSN = "mysql://secret-user:secret-password@localhost/scert"
	configPath := dir + "\\config\\scert.json"
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "server": {"port": 5443},
        "web": {"hostname": "admin.example.test"},
        "acme": {"renewal_threshold_days": 10}
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("更新设置应成功，实际为 %d：%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, secret := range []string{"password_hash", "secret-password", "credential_ref", "tls_key", "mysql://"} {
		if strings.Contains(body, secret) {
			t.Fatalf("响应不应包含敏感字段 %q：%s", secret, body)
		}
	}
	var payload struct {
		Settings        settingsView `json:"settings"`
		RestartRequired bool         `json:"restart_required"`
		Message         string       `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析设置响应失败：%v", err)
	}
	if !payload.RestartRequired || payload.Settings.Server.Port != 5443 || payload.Settings.Web.Hostname != "admin.example.test" || payload.Settings.ACME.RenewalThresholdDays != 10 {
		t.Fatalf("设置响应内容不正确：%+v", payload.Settings)
	}
	if !strings.Contains(payload.Message, "请重启 Scert 服务") {
		t.Fatalf("监听参数变更后应明确提示重启服务：%s", payload.Message)
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("读取保存后的配置失败：%v", err)
	}
	if saved.Admin.PasswordHash != cfg.Admin.PasswordHash || saved.Database.DSN != cfg.Database.DSN {
		t.Fatal("更新设置不应覆盖管理员密码或数据库 DSN")
	}
}

func TestSettingsAPIWebAddressChangeDoesNotRequireRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	configPath := dir + "\\config\\scert.json"
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "web": {"hostname": "admin.example.test", "public_ip": "203.0.113.10"}
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("更新 Web 访问地址应成功，实际为 %d：%s", response.Code, response.Body.String())
	}
	var payload struct {
		RestartRequired bool   `json:"restart_required"`
		Message         string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析设置响应失败：%v", err)
	}
	if payload.RestartRequired {
		t.Fatal("仅修改 Web 域名或公网 IP 不应要求重启服务")
	}
	if payload.Message != "设置已保存" {
		t.Fatalf("无需重启时应返回简洁保存提示：%s", payload.Message)
	}
}

func TestSettingsAPIReturnsChineseNotificationCredentialError(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	configPath := dir + "\\config\\scert.json"
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "notification": {"smtp_password": "secret-password"}
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("敏感凭据存储未配置时应返回 400，实际为 %d：%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "保存通知凭据失败") {
		t.Fatalf("响应应包含中文通知凭据错误说明：%s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "???") {
		t.Fatalf("响应不应包含问号乱码：%s", response.Body.String())
	}
}

func TestRenewalAPIUsesConfiguredRunner(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	called := false
	server := New(Dependencies{
		Config: cfg,
		RunRenewal: func(context.Context) (certificate.RenewalReport, error) {
			called = true
			return certificate.RenewalReport{Checked: 2, Selected: 1, Succeeded: 1}, nil
		},
	})
	cookie := loginForSettingsTest(t, server)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/renewal/run", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !called || !strings.Contains(response.Body.String(), `"selected":1`) {
		t.Fatalf("续期接口调用结果不正确：%d %s；called=%v", response.Code, response.Body.String(), called)
	}
}

func loginForSettingsTest(t *testing.T, server *Server) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"identifier":"admin","password":"Password123"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("管理员登录失败：%d %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("登录响应未返回会话 Cookie")
	}
	return cookies[0]
}

func mustTestPasswordHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成测试密码摘要失败：%v", err)
	}
	return string(hash)
}
