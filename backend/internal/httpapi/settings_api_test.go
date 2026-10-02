package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
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

func TestSettingsAPIUpdateRequiresSuperAdmin(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	server := New(Dependencies{Config: cfg})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{"server":{"port":5443}}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(httpUserSessionCookie(server, "settings-admin", repository.UserRoleAdmin))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("普通管理员修改系统设置应返回 403，实际为 %d：%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "只有超级管理员可以管理系统设置") {
		t.Fatalf("系统设置权限错误提示不明确：%s", response.Body.String())
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
	configPath := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "server": {"port": 5443, "proxy_https_port": 8004},
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
	if payload.Settings.Server.ProxyHTTPSPort != 8004 || saved.Server.ProxyHTTPSPort != 8004 {
		t.Fatal("代理公开端口未保存或未返回")
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
	configPath := filepath.Join(dir, "config", "scert.json")
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

func TestSettingsAPIWebEnabledChangeRequiresRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.Web.Enabled = true
	cfg.Web.Hostname = "scert.example.test"
	cfg.DNS.VerificationState = "verified"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	configPath := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "web": {"enabled": false}
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("更新 Web 页面开关应成功，实际为 %d：%s", response.Code, response.Body.String())
	}
	var payload struct {
		Settings        settingsView `json:"settings"`
		RestartRequired bool         `json:"restart_required"`
		Message         string       `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析 Web 页面开关响应失败：%v", err)
	}
	if !payload.RestartRequired || payload.Settings.Web.Enabled {
		t.Fatalf("关闭 Web 页面应要求重启并返回关闭状态：%+v", payload)
	}
	if !strings.Contains(payload.Message, "Web 页面") || !strings.Contains(payload.Message, "请重启 Scert 服务") {
		t.Fatalf("Web 页面开关变更应明确提示重启服务：%s", payload.Message)
	}

	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("读取 Web 页面开关配置失败：%v", err)
	}
	if saved.Web.Enabled {
		t.Fatal("关闭 Web 页面后配置文件仍为开启状态")
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
	configPath := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	var applied certificate.RenewalPolicy
	appliedCalled := false
	server := New(Dependencies{
		Config:     cfg,
		ConfigPath: configPath,
		UpdateRenewalPolicy: func(policy certificate.RenewalPolicy) {
			applied = policy
			appliedCalled = true
		},
	})
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
	if payload.Restart || payload.Settings.ACME.RenewalCheckIntervalDays != 2 || payload.Settings.ACME.RenewalNearExpiryDays != 14 || payload.Settings.ACME.RenewalThresholdDays != 7 || payload.Settings.ACME.RenewalUrgentThresholdDays != 2 || payload.Settings.ACME.RenewalMaxRetries != 5 || payload.Settings.ACME.RenewalRetryIntervalMins != 45 {
		t.Fatalf("更新后的续期策略不正确：restart=%v acme=%+v", payload.Restart, payload.Settings.ACME)
	}
	if !appliedCalled {
		t.Fatal("\u4fdd\u5b58\u7eed\u671f\u7b56\u7565\u540e\u5e94\u7acb\u5373\u8c03\u7528\u8fd0\u884c\u65f6\u66f4\u65b0\u56de\u8c03")
	}
	if applied.CheckInterval != 2*24*time.Hour || applied.NearExpiryThreshold != 14*24*time.Hour || applied.RenewalThreshold != 7*24*time.Hour || applied.UrgentThreshold != 2*24*time.Hour || applied.MaxRetries != 5 || applied.RetryInterval != 45*time.Minute {
		t.Fatalf("\u8fd0\u884c\u65f6\u7eed\u671f\u7b56\u7565\u672a\u6b63\u786e\u8f6c\u6362\uff1a%+v", applied)
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
	configPath := filepath.Join(dir, "config", "scert.json")
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
	configPath := filepath.Join(dir, "config", "scert.json")
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
	server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	server.runRenewal = func(context.Context) (certificate.RenewalReport, error) {
		return certificate.RenewalReport{Checked: 2, Selected: 1, Succeeded: 1}, nil
	}
	response := httptest.NewRecorder()
	serveSuperAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/renewal/run", nil))
	task := submittedTask(t, response)
	waitTaskWorkersForTest(t, server)
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "success" || !strings.Contains(stored.Message, "检查 2 项") || !strings.Contains(stored.Message, "成功 1 项") {
		t.Fatalf("续期任务结果不正确：%+v %v", stored, err)
	}
}

func TestRenewalAPIHidesWorkflowErrors(t *testing.T) {
	server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	server.runRenewal = func(context.Context) (certificate.RenewalReport, error) {
		return certificate.RenewalReport{}, errors.New("private-key-secret-path")
	}
	response := httptest.NewRecorder()
	serveSuperAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/renewal/run", nil))
	task := submittedTask(t, response)
	waitTaskWorkersForTest(t, server)
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "failed" || strings.Contains(stored.Message, "private-key-secret-path") || !strings.Contains(stored.Message, "未全部完成") {
		t.Fatalf("续期失败任务未安全保留结果：%+v %v", stored, err)
	}
}
func TestRenewalAPIRequiresSuperAdmin(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	runCalls := 0
	server := New(Dependencies{
		Config: cfg,
		RunRenewal: func(context.Context) (certificate.RenewalReport, error) {
			runCalls++
			return certificate.RenewalReport{}, nil
		},
	})
	cookie := httpUserSessionCookie(server, "renewal-admin", repository.UserRoleAdmin)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/renewal/run", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-super-admin renewal request returned %d: %s", response.Code, response.Body.String())
	}
	if runCalls != 0 {
		t.Fatalf("renewal runner was called for a non-super-admin: %d", runCalls)
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
	configPath := filepath.Join(dir, "config", "scert.json")
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
	configPath := filepath.Join(dir, "config", "scert.json")
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
	configPath := filepath.Join(dir, "config", "scert.json")
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

func TestSettingsAPIDNSRequiresCompleteVerification(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.DNS.Provider = "cloudflare"
	cfg.DNS.CredentialRef = "secret://dns/cloudflare/old"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	configPath := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	server := New(Dependencies{
		Config: cfg, ConfigPath: configPath,
		SetupDNS: &fakeSetupDNSVerifier{},
	})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "dns": {"provider": "cloudflare", "verification_token": "token-only"}
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("DNS 验证参数不完整应返回 400，实际为 %d：%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "保存 DNS 设置失败") {
		t.Fatalf("错误响应应说明 DNS 设置保存失败：%s", response.Body.String())
	}
}

func TestSettingsAPIDNSProviderChangeRequiresReverification(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.DNS.Provider = "cloudflare"
	cfg.DNS.CredentialRef = "secret://dns/cloudflare/old"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	configPath := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "dns": {"provider": "aliyun"}
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("更换 DNS 服务商但未重新验证应返回 400，实际为 %d：%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "设置内容校验失败") {
		t.Fatalf("错误响应应说明设置校验失败：%s", response.Body.String())
	}
}

func TestSettingsAPISavesVerifiedDNSCredentialWithoutReturningSecret(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.DNS.Provider = "cloudflare"
	cfg.DNS.CredentialRef = "secret://dns/cloudflare/old"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	configPath := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	const newCredentialRef = "secret://dns/cloudflare/new"
	verifier := &fakeSetupDNSVerifier{verifyResult: setup.DNSVerificationResult{
		CredentialRef: newCredentialRef, VerificationToken: "new-verification-token",
	}}
	secrets := &fakeSetupSecretWriter{values: map[string]map[string]string{
		cfg.DNS.CredentialRef: {"api_token": "old-token"},
		newCredentialRef:      {"api_token": "new-token"},
	}}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath, SetupDNS: verifier, Secrets: secrets})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "dns": {
            "provider": "cloudflare",
            "credential_ref": "secret://dns/cloudflare/new",
            "verification_token": "new-verification-token",
            "test_domain": "example.com"
        }
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("保存已验证 DNS 设置应成功，实际为 %d：%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, secret := range []string{"new-verification-token", newCredentialRef, "new-token", "credential_ref"} {
		if strings.Contains(body, secret) {
			t.Fatalf("设置响应不应返回 DNS 敏感内容 %q：%s", secret, body)
		}
	}
	if !verifier.consumed {
		t.Fatal("保存成功后应消费 DNS 验证令牌")
	}
	if _, ok := secrets.values[cfg.DNS.CredentialRef]; ok {
		t.Fatal("替换后的旧 DNS 凭据应被清理")
	}
	if _, ok := secrets.values[newCredentialRef]; !ok {
		t.Fatal("新的 DNS 凭据应继续保留")
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("读取保存后的配置失败：%v", err)
	}
	if saved.DNS.CredentialRef != newCredentialRef || saved.DNS.VerificationState != "verified" {
		t.Fatalf("DNS 配置没有正确持久化：%+v", saved.DNS)
	}
}

func TestSettingsAPIDNSCheckFailureRollsBackCredential(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.DNS.Provider = "cloudflare"
	cfg.DNS.CredentialRef = "secret://dns/cloudflare/old"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	configPath := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	const newCredentialRef = "secret://dns/cloudflare/new"
	verifier := &fakeSetupDNSVerifier{checkErr: errors.New("DNS 验证令牌无效")}
	secrets := &fakeSetupSecretWriter{values: map[string]map[string]string{
		newCredentialRef: {"api_token": "new-token"},
	}}
	server := New(Dependencies{Config: cfg, ConfigPath: configPath, SetupDNS: verifier, Secrets: secrets})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "dns": {
            "provider": "cloudflare",
            "credential_ref": "secret://dns/cloudflare/new",
            "verification_token": "invalid-token",
            "test_domain": "example.com"
        }
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("无效 DNS 验证令牌应返回 400，实际为 %d：%s", response.Code, response.Body.String())
	}
	if _, ok := secrets.values[newCredentialRef]; ok {
		t.Fatal("DNS 验证失败后不应保留新凭据")
	}
	if len(secrets.deleted) != 1 || secrets.deleted[0] != newCredentialRef {
		t.Fatalf("DNS 验证失败后应删除新凭据：%v", secrets.deleted)
	}
}

func TestSettingsAPIDNSSaveFailureRollsBackCredential(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.DNS.Provider = "cloudflare"
	cfg.DNS.CredentialRef = "secret://dns/cloudflare/old"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = mustTestPasswordHash(t, "Password123")
	initialConfigPath := filepath.Join(dir, "config", "initial.json")
	if err := cfg.Save(initialConfigPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	blockedPath := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blockedPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("创建保存失败测试文件失败：%v", err)
	}
	const newCredentialRef = "secret://dns/cloudflare/new"
	verifier := &fakeSetupDNSVerifier{verifyResult: setup.DNSVerificationResult{
		CredentialRef: newCredentialRef, VerificationToken: "new-verification-token",
	}}
	secrets := &fakeSetupSecretWriter{values: map[string]map[string]string{
		newCredentialRef: {"api_token": "new-token"},
	}}
	server := New(Dependencies{Config: cfg, ConfigPath: filepath.Join(blockedPath, "scert.json"), SetupDNS: verifier, Secrets: secrets})
	cookie := loginForSettingsTest(t, server)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{
        "dns": {
            "provider": "cloudflare",
            "credential_ref": "secret://dns/cloudflare/new",
            "verification_token": "new-verification-token",
            "test_domain": "example.com"
        }
    }`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("配置保存失败应返回 500，实际为 %d：%s", response.Code, response.Body.String())
	}
	if _, ok := secrets.values[newCredentialRef]; ok {
		t.Fatal("配置保存失败后不应保留新 DNS 凭据")
	}
	if len(secrets.deleted) != 1 || secrets.deleted[0] != newCredentialRef {
		t.Fatalf("配置保存失败后应删除新 DNS 凭据：%v", secrets.deleted)
	}
	if verifier.consumed {
		t.Fatal("配置保存失败后不应消费 DNS 验证令牌")
	}
}
