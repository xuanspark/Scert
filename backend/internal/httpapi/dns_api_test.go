package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
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
	serveAdminHTTP(server, response, request)
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
	serveAdminHTTP(server, response, request)
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
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("DNS 服务未初始化时应返回 503，实际为 %d：%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"verification_timeout_secs":60`) {
		t.Fatalf("DNS 服务未初始化响应应包含当前最大等待时间：%s", response.Body.String())
	}
}
