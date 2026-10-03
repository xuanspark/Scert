package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

func TestAdminDNSProvidersRequiresAuthentication(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	server := New(Dependencies{Config: cfg, SetupDNS: &fakeSetupDNSVerifier{}})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/dns/providers", nil)
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问 DNS 服务商接口应返回 401，实际为 %d：%s", response.Code, response.Body.String())
	}
}

func TestAdminDNSProvidersReturnsCatalog(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	server := New(Dependencies{Config: cfg, SetupDNS: &fakeSetupDNSVerifier{}})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/dns/providers", nil)
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("读取 DNS 服务商目录失败：%d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Cloudflare") || !strings.Contains(response.Body.String(), "credential_fields") {
		t.Fatalf("DNS 服务商目录内容不完整：%s", response.Body.String())
	}
}

func TestAdminDNSMutationRequiresSuperAdmin(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	server := New(Dependencies{Config: cfg, SetupDNS: &fakeSetupDNSVerifier{}})
	cookie := httpUserSessionCookie(server, "dns-admin", repository.UserRoleAdmin)
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "在线测试",
			method: http.MethodPost,
			path:   "/api/v1/dns/test",
			body:   `{"provider":"cloudflare","test_domain":"example.com","credentials":{"api_token":"test-token"}}`,
		},
		{
			name:   "阿里云权限检查",
			method: http.MethodPost,
			path:   "/api/v1/dns/aliyun/check",
			body:   `{"domain":"example.com"}`,
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(item.method, item.path, strings.NewReader(item.body))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("普通管理员执行 DNS 变更操作应返回 403，实际为 %d：%s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), "只有超级管理员可以管理系统设置") {
				t.Fatalf("DNS 权限错误提示不明确：%s", response.Body.String())
			}
		})
	}
}

func TestAdminDNSAccountsExposeProjectScope(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.DNS.ProjectID = "project-a"
	cfg.DNS.CredentialRef = "secret://dns/cloudflare/project-a"
	secrets := &fakeSecretReaderStore{data: map[string]map[string]string{
		cfg.DNS.CredentialRef: {"api_token": "test-token"},
	}}
	server := New(Dependencies{Config: cfg, Secrets: secrets, ProjectDomains: projectDomainRepositoryStub{}})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/dns/accounts", nil)
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("读取 DNS 账户失败：%d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"project_id":"project-a"`) {
		t.Fatalf("DNS 账户响应未包含项目归属：%s", response.Body.String())
	}
}

func TestDNSCredentialUseRejectsUnauthorizedProject(t *testing.T) {
	server := New(Dependencies{
		ProjectDomains: projectDomainRepositoryStub{authorizeErr: repository.ErrProjectAccessDenied},
	})
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	context.Set(authenticatedActorKey, "user-a")
	if server.authorizeDNSCredentialUse(context, "project-a") {
		t.Fatal("无 DNS 凭据使用权限的项目不应通过授权")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("无 DNS 凭据使用权限应返回 403，实际为 %d", recorder.Code)
	}
}

func TestSettingsDNSProjectAssignmentRequiresDNSManagePermission(t *testing.T) {
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
	server := New(Dependencies{
		Config: cfg, ConfigPath: configPath,
		ProjectDomains: projectDomainRepositoryStub{authorizeErr: repository.ErrProjectAccessDenied},
	})
	cookie := loginForSettingsTest(t, server)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{"dns":{"project_id":"project-a"}}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("无 DNS 管理权限修改项目归属应返回 403，实际为 %d：%s", response.Code, response.Body.String())
	}
	if server.config.DNS.ProjectID != "default" {
		t.Fatalf("拒绝修改后 DNS 项目归属不应变化：%q", server.config.DNS.ProjectID)
	}
}

func TestAdminDNSTestReturnsVerificationResult(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	verifier := &fakeSetupDNSVerifier{}
	server := New(Dependencies{Config: cfg, SetupDNS: verifier})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/dns/test", strings.NewReader(`{
		"provider":"cloudflare",
		"test_domain":"example.com",
		"credentials":{"api_token":"test-token"}
	}`))
	request.Header.Set("Content-Type", "application/json")
	serveSuperAdminHTTP(server, response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("DNS 在线测试失败：%d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Verified                bool                        `json:"verified"`
		CredentialRef           string                      `json:"credential_ref"`
		VerificationToken       string                      `json:"verification_token"`
		TestReport              setup.DNSVerificationReport `json:"test_report"`
		VerificationTimeoutSecs int                         `json:"verification_timeout_secs"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析 DNS 在线测试响应失败：%v", err)
	}
	if !payload.Verified || payload.CredentialRef == "" || payload.VerificationToken == "" || payload.TestReport.RecordName == "" || payload.VerificationTimeoutSecs <= 0 {
		t.Fatalf("DNS 在线测试响应内容不正确：%+v", payload)
	}
}

func TestAdminAliyunDNSTestReportsFastVerificationTimeout(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	server := New(Dependencies{Config: cfg, SetupDNS: &fakeSetupDNSVerifier{}})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/dns/test", strings.NewReader(`{
		"provider":"aliyun",
		"test_domain":"example.com",
		"credentials":{"access_key_id":"test-id","access_key_secret":"test-secret","managed_zones":"example.com","allowed_domains":"example.com"}
	}`))
	request.Header.Set("Content-Type", "application/json")
	serveSuperAdminHTTP(server, response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"verification_timeout_secs":6`) {
		t.Fatalf("管理端阿里云 DNS 在线测试未返回 6 秒等待上限：%d %s", response.Code, response.Body.String())
	}
}

func TestAdminDNSTestRejectsUnavailableService(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	server := New(Dependencies{Config: cfg})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/dns/test", strings.NewReader(`{"provider":"cloudflare","test_domain":"example.com","credentials":{"api_token":"test-token"}}`))
	request.Header.Set("Content-Type", "application/json")
	serveSuperAdminHTTP(server, response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("DNS 服务未初始化时应返回 503，实际为 %d：%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"verification_timeout_secs":60`) {
		t.Fatalf("DNS 服务未初始化响应应包含当前最大等待时间：%s", response.Body.String())
	}
}
