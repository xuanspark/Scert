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

func TestSettingsAPIReturnsAndUpdatesRenewalPolicy(t *testing.T) {
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

	getRequest := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	getRequest.AddCookie(cookie)
	getResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("读取设置应成功，实际为 %d：%s", getResponse.Code, getResponse.Body.String())
	}
	var initial struct {
		ACME settingsACMEView `json:"acme"`
	}
	if err := json.Unmarshal(getResponse.Body.Bytes(), &initial); err != nil {
		t.Fatalf("解析初始设置失败：%v", err)
	}
	if initial.ACME.RenewalCheckIntervalDays != 3 || initial.ACME.RenewalNearExpiryDays != 10 || initial.ACME.RenewalThresholdDays != 6 || initial.ACME.RenewalUrgentThresholdDays != 3 || initial.ACME.RenewalMaxRetries != 3 || initial.ACME.RenewalRetryIntervalMins != 30 {
		t.Fatalf("初始续期策略返回不完整：%+v", initial.ACME)
	}

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "acme": {
            "renewal_check_interval_days": 2,
            "renewal_near_expiry_days": 14,
            "renewal_threshold_days": 7,
            "renewal_urgent_threshold_days": 2,
            "renewal_max_retries": 5,
            "renewal_retry_interval_mins": 45
        }
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("更新续期策略应成功，实际为 %d：%s", response.Code, response.Body.String())
	}
	var payload struct {
		Settings settingsView `json:"settings"`
		Restart  bool         `json:"restart_required"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析续期策略响应失败：%v", err)
	}
	if !payload.Restart || payload.Settings.ACME.RenewalCheckIntervalDays != 2 || payload.Settings.ACME.RenewalNearExpiryDays != 14 || payload.Settings.ACME.RenewalThresholdDays != 7 || payload.Settings.ACME.RenewalUrgentThresholdDays != 2 || payload.Settings.ACME.RenewalMaxRetries != 5 || payload.Settings.ACME.RenewalRetryIntervalMins != 45 {
		t.Fatalf("更新后的续期策略不正确：restart=%v acme=%+v", payload.Restart, payload.Settings.ACME)
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("读取续期策略配置失败：%v", err)
	}
	if saved.ACME.RenewalCheckIntervalDays != 2 || saved.ACME.RenewalNearExpiryDays != 14 || saved.ACME.RenewalThresholdDays != 7 || saved.ACME.RenewalUrgentThresholdDays != 2 || saved.ACME.RenewalMaxRetries != 5 || saved.ACME.RenewalRetryIntervalMins != 45 {
		t.Fatalf("续期策略没有正确持久化：%+v", saved.ACME)
	}
}

func TestSettingsAPIRejectsInvalidRenewalPolicy(t *testing.T) {
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
        "acme": {
            "renewal_near_expiry_days": 2,
            "renewal_threshold_days": 7
        }
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("非法续期阈值关系应返回 400，实际为 %d：%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "设置内容校验失败") {
		t.Fatalf("错误响应应说明设置校验失败：%s", response.Body.String())
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("读取回滚后的配置失败：%v", err)
	}
	if saved.ACME.RenewalNearExpiryDays != cfg.ACME.RenewalNearExpiryDays || saved.ACME.RenewalThresholdDays != cfg.ACME.RenewalThresholdDays {
		t.Fatalf("非法设置不应持久化：before=%+v after=%+v", cfg.ACME, saved.ACME)
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

func TestSettingsAPIRollsBackCredentialsWhenValidationFails(t *testing.T) {
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
		t.Fatalf("save test config: %v", err)
	}
	secrets := &fakeSetupSecretWriter{}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath, Secrets: secrets})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "notification": {
            "http_auth_mode": "bearer",
            "http_credentials": {"token": "new-token"},
            "http_path": "email/send"
        }
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid notification settings should fail: %d %s", response.Code, response.Body.String())
	}
	if len(secrets.values) != 0 || len(secrets.deleted) != 1 {
		t.Fatalf("new credentials should be rolled back after validation failure: values=%v deleted=%v", secrets.values, secrets.deleted)
	}
}

func TestSettingsAPICleansReplacedNotificationCredential(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	cfg.Notification.HTTPAuthMode = "bearer"
	cfg.Notification.HTTPCredentialRef = "secret://notification/old"
	configPath := dir + "\\config\\scert.json"
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("save test config: %v", err)
	}
	secrets := &fakeSetupSecretWriter{values: map[string]map[string]string{
		"secret://notification/old": {"token": "old-token"},
	}}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath, Secrets: secrets})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "notification": {
            "http_auth_mode": "bearer",
            "http_credentials": {"token": "new-token"}
        }
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("notification credential replacement should succeed: %d %s", response.Code, response.Body.String())
	}
	if _, ok := secrets.values["secret://notification/old"]; ok {
		t.Fatal("replaced notification credential should be deleted")
	}
	if len(secrets.values) != 1 {
		t.Fatalf("new notification credential should remain: %v", secrets.values)
	}
	if strings.Contains(response.Body.String(), "new-token") {
		t.Fatal("notification credential must not be returned in the response")
	}
}

func TestSettingsAPIRollsBackEarlierCredentialWhenLaterCredentialFails(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	cfg.Notification.SMTPUsername = "smtp-user"
	configPath := dir + "\\config\\scert.json"
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("save test config: %v", err)
	}
	secrets := &fakeSetupSecretWriter{}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath, Secrets: secrets})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "notification": {
            "http_auth_mode": "bearer",
            "http_credentials": {"token": "new-token"},
            "smtp_enabled": true,
            "smtp_username": "",
            "smtp_password": "smtp-password"
        }
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("later SMTP validation should fail: %d %s", response.Code, response.Body.String())
	}
	if len(secrets.values) != 0 || len(secrets.deleted) != 1 {
		t.Fatalf("earlier HTTP credentials should be rolled back: values=%v deleted=%v", secrets.values, secrets.deleted)
	}
}
