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

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

type fakeSetupDNSVerifier struct {
	verifyResult setup.DNSVerificationResult
	verifyErr    error
	checkErr     error
	consumeErr   error
	checked      bool
	consumed     bool
}

type fakeFirewallManager struct {
	result       firewall.Result
	openErr      error
	rollbackErr  error
	openedPort   int
	rollbackCall bool
}

func (f *fakeFirewallManager) OpenTCP(_ context.Context, port int) (firewall.Result, error) {
	f.openedPort = port
	if f.result.Port == 0 {
		f.result.Port = port
	}
	return f.result, f.openErr
}

func (f *fakeFirewallManager) Rollback(_ context.Context, result firewall.Result) error {
	f.rollbackCall = true
	if !result.Created {
		return errors.New("不应回滚未创建的防火墙规则")
	}
	return f.rollbackErr
}

func (f *fakeSetupDNSVerifier) Verify(context.Context, setup.DNSVerificationInput) (setup.DNSVerificationResult, error) {
	if f.verifyResult.CredentialRef == "" {
		f.verifyResult = setup.DNSVerificationResult{
			CredentialRef:     "secret://dns/cloudflare/test",
			VerificationToken: "verified-token",
			ExpiresAt:         time.Now().UTC().Add(15 * time.Minute),
		}
	}
	return f.verifyResult, f.verifyErr
}

func (f *fakeSetupDNSVerifier) Check(token, providerID, credentialRef, testDomain string) error {
	if f.checkErr != nil {
		return f.checkErr
	}
	if token == "" || providerID == "" || credentialRef == "" || testDomain == "" {
		return errors.New("DNS 验证参数不完整")
	}
	f.checked = true
	return nil
}

func (f *fakeSetupDNSVerifier) Consume(token, providerID, credentialRef, testDomain string) error {
	if f.consumeErr != nil {
		return f.consumeErr
	}
	if err := f.Check(token, providerID, credentialRef, testDomain); err != nil {
		return err
	}
	f.consumed = true
	return nil
}

func TestSetupStatusOnlyAllowsLocalAccessBeforeInitialization(t *testing.T) {
	cfg := config.Default(t.TempDir())
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, SetupDNS: &fakeSetupDNSVerifier{}})

	local := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	local.RemoteAddr = "127.0.0.1:53000"
	localResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(localResponse, local)
	if localResponse.Code != http.StatusOK {
		t.Fatalf("本机读取初始化状态失败：%d %s", localResponse.Code, localResponse.Body.String())
	}
	if !strings.Contains(localResponse.Body.String(), `"dns_test_available":true`) {
		t.Fatalf("初始化状态应显示 DNS 在线测试可用：%s", localResponse.Body.String())
	}

	remote := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	remote.RemoteAddr = "203.0.113.8:53000"
	remoteResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(remoteResponse, remote)
	if remoteResponse.Code != http.StatusForbidden {
		t.Fatalf("远程访问初始化状态应被拒绝：%d", remoteResponse.Code)
	}
}

func TestSetupDNSProviderCatalogAndOnlineTest(t *testing.T) {
	cfg := config.Default(t.TempDir())
	verifier := &fakeSetupDNSVerifier{}
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, SetupDNS: verifier})

	catalogRequest := httptest.NewRequest(http.MethodGet, "/api/v1/setup/dns/providers", nil)
	catalogRequest.RemoteAddr = "127.0.0.1:53000"
	catalogResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(catalogResponse, catalogRequest)
	if catalogResponse.Code != http.StatusOK || !strings.Contains(catalogResponse.Body.String(), "cloudflare") {
		t.Fatalf("读取 DNS 服务商目录失败：%d %s", catalogResponse.Code, catalogResponse.Body.String())
	}

	payload := `{"provider":"cloudflare","test_domain":"example.com","credentials":{"api_token":"secret-token"}}`
	testRequest := httptest.NewRequest(http.MethodPost, "/api/v1/setup/dns/test", strings.NewReader(payload))
	testRequest.RemoteAddr = "127.0.0.1:53000"
	testRequest.Header.Set("Content-Type", "application/json")
	testResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(testResponse, testRequest)
	if testResponse.Code != http.StatusOK {
		t.Fatalf("DNS 在线测试失败：%d %s", testResponse.Code, testResponse.Body.String())
	}
	if strings.Contains(testResponse.Body.String(), "secret-token") {
		t.Fatal("DNS 在线测试响应不应返回明文凭据")
	}
}

func TestSetupApplyPersistsConfigurationWithoutPlainPassword(t *testing.T) {
	baseDir := t.TempDir()
	cfg := config.Default(baseDir)
	configPath := filepath.Join(baseDir, "config", "scert.json")
	verifier := &fakeSetupDNSVerifier{}
	firewallManager := &fakeFirewallManager{result: firewall.Result{
		Backend: "windows", RuleName: "Scert Web TCP 8443", Port: 8443, Created: true, Message: "已创建测试规则",
	}}
	server := New(Dependencies{
		Config: cfg, ConfigPath: configPath, Repository: &stubDomainRepository{},
		SetupDNS: verifier, Firewall: firewallManager,
	})
	payload := `{
		"admin_username":"root-admin",
		"admin_email":"admin@example.test",
		"admin_password":"secure-pass-123",
		"server_port":8443,
		"email_provider":"mail-api",
		"email_endpoint":"https://mail.example.test/send",
		"recipients":["ops@example.test","ops@example.test"],
		"dns_provider":"cloudflare",
		"dns_credential_ref":"secret://dns/cloudflare/default",
		"dns_verification_token":"verified-token",
		"dns_test_domain":"scert.example.test",
		"certificate_root":"` + filepath.ToSlash(filepath.Join(baseDir, "ssl")) + `",
		"web_hostname":"scert.example.test",
		"web_enabled":true,
		"https_mode":"direct"
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(payload))
	request.RemoteAddr = "127.0.0.1:53000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("提交初始化配置失败：%d %s", response.Code, response.Body.String())
	}
	if !verifier.consumed {
		t.Fatal("初始化提交前应消费 DNS 在线验证令牌")
	}
	if firewallManager.openedPort != 8443 || firewallManager.rollbackCall {
		t.Fatalf("应只开放主服务端口且成功时不回滚：port=%d rollback=%t", firewallManager.openedPort, firewallManager.rollbackCall)
	}
	if !strings.Contains(response.Body.String(), `"firewall"`) || !strings.Contains(response.Body.String(), `"created":true`) {
		t.Fatalf("初始化响应应返回本机防火墙结果：%s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"reset_url":"https://scert.example.test/reset"`) {
		t.Fatalf("初始化响应应返回根据 Web 地址生成的密码重置路径：%s", response.Body.String())
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取保存后的配置失败：%v", err)
	}
	if strings.Contains(string(data), "secure-pass-123") {
		t.Fatal("配置文件不应保存管理员明文密码")
	}
	var saved config.Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("解析保存后的配置失败：%v", err)
	}
	if !saved.SetupReady || saved.Admin.Username != "root-admin" || saved.Admin.Email != "admin@example.test" || saved.Server.Port != 8443 {
		t.Fatalf("保存后的初始化配置不正确：%+v", saved)
	}
	if len(saved.Notification.Recipients) != 1 {
		t.Fatalf("通知收件人未去重：%v", saved.Notification.Recipients)
	}

	repeat := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(payload))
	repeat.RemoteAddr = "127.0.0.1:53001"
	repeat.Header.Set("Content-Type", "application/json")
	repeatResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(repeatResponse, repeat)
	if repeatResponse.Code != http.StatusConflict {
		t.Fatalf("重复初始化应返回冲突：%d", repeatResponse.Code)
	}
}

func TestSetupApplyRejectsUnverifiedDNSConfiguration(t *testing.T) {
	cfg := config.Default(t.TempDir())
	verifier := &fakeSetupDNSVerifier{checkErr: errors.New("DNS 验证令牌无效或已经过期，请重新测试")}
	server := New(Dependencies{Config: cfg, ConfigPath: filepath.Join(t.TempDir(), "scert.json"), Repository: &stubDomainRepository{}, SetupDNS: verifier})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(`{"dns_provider":"cloudflare"}`))
	request.RemoteAddr = "127.0.0.1:53000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "DNS 验证令牌") {
		t.Fatalf("未验证 DNS 配置应被拒绝：%d %s", response.Code, response.Body.String())
	}
}

func TestSetupApplyRejectsOversizedRequest(t *testing.T) {
	cfg := config.Default(t.TempDir())
	server := New(Dependencies{
		Config: cfg, ConfigPath: filepath.Join(t.TempDir(), "scert.json"),
		Repository: &stubDomainRepository{}, SetupDNS: &fakeSetupDNSVerifier{},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(`{"padding":"`+strings.Repeat("x", setupRequestLimit)+`"}`))
	request.RemoteAddr = "127.0.0.1:53000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("超大初始化请求应被拒绝：%d", response.Code)
	}
}

func TestSetupDNSTestRejectsRemoteAndOversizedRequests(t *testing.T) {
	cfg := config.Default(t.TempDir())
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, SetupDNS: &fakeSetupDNSVerifier{}})

	remoteRequest := httptest.NewRequest(http.MethodPost, "/api/v1/setup/dns/test", strings.NewReader(`{"provider":"cloudflare"}`))
	remoteRequest.RemoteAddr = "203.0.113.8:53000"
	remoteRequest.Header.Set("Content-Type", "application/json")
	remoteResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(remoteResponse, remoteRequest)
	if remoteResponse.Code != http.StatusForbidden {
		t.Fatalf("远程 DNS 测试请求应被拒绝：%d %s", remoteResponse.Code, remoteResponse.Body.String())
	}

	oversizedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/setup/dns/test", strings.NewReader(`{"provider":"cloudflare","padding":"`+strings.Repeat("x", setupRequestLimit)+`"}`))
	oversizedRequest.RemoteAddr = "127.0.0.1:53000"
	oversizedRequest.Header.Set("Content-Type", "application/json")
	oversizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(oversizedResponse, oversizedRequest)
	if oversizedResponse.Code != http.StatusBadRequest {
		t.Fatalf("超大 DNS 测试请求应被拒绝：%d %s", oversizedResponse.Code, oversizedResponse.Body.String())
	}
}

func TestSetupDNSTestReturnsNotImplementedForUnavailableProvider(t *testing.T) {
	cfg := config.Default(t.TempDir())
	verifier := &fakeSetupDNSVerifier{verifyErr: dnsprovider.ErrProviderUnavailable}
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, SetupDNS: verifier})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup/dns/test", strings.NewReader(`{"provider":"aliyun","test_domain":"example.com","credentials":{"access_key_id":"id","access_key_secret":"secret"}}`))
	request.RemoteAddr = "127.0.0.1:53000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "适配器尚未开放") {
		t.Fatalf("未开放 DNS Provider 应返回 501：%d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "access_key_secret") || strings.Contains(response.Body.String(), "secret") {
		t.Fatal("未开放 Provider 响应不应包含提交的凭据")
	}
}

func TestSetupValidationFailureDoesNotConsumeDNSToken(t *testing.T) {
	baseDir := t.TempDir()
	verifier := &fakeSetupDNSVerifier{}
	server := New(Dependencies{
		Config: config.Default(baseDir), ConfigPath: filepath.Join(baseDir, "config", "scert.json"),
		Repository: &stubDomainRepository{}, SetupDNS: verifier,
	})
	payload := setupPayload(baseDir, "short")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(payload))
	request.RemoteAddr = "127.0.0.1:53000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("无效初始化配置应被拒绝：%d %s", response.Code, response.Body.String())
	}
	if !verifier.checked || verifier.consumed {
		t.Fatalf("配置校验失败时应检查但不消费 DNS 令牌：checked=%t consumed=%t", verifier.checked, verifier.consumed)
	}
}

func TestSetupSaveFailureDoesNotConsumeDNSToken(t *testing.T) {
	baseDir := t.TempDir()
	blockedParent := filepath.Join(baseDir, "blocked")
	if err := os.WriteFile(blockedParent, []byte("占位"), 0o600); err != nil {
		t.Fatalf("创建阻塞路径失败：%v", err)
	}
	verifier := &fakeSetupDNSVerifier{}
	firewallManager := &fakeFirewallManager{result: firewall.Result{
		Backend: "ufw", RuleName: "Scert Web TCP 8443", Port: 8443, Created: true,
	}}
	server := New(Dependencies{
		Config: config.Default(baseDir), ConfigPath: filepath.Join(blockedParent, "scert.json"),
		Repository: &stubDomainRepository{}, SetupDNS: verifier, Firewall: firewallManager,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(setupPayload(baseDir, "secure-pass-123")))
	request.RemoteAddr = "127.0.0.1:53000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("配置保存失败应返回服务器错误：%d %s", response.Code, response.Body.String())
	}
	if !verifier.checked || verifier.consumed {
		t.Fatalf("配置保存失败时应检查但不消费 DNS 令牌：checked=%t consumed=%t", verifier.checked, verifier.consumed)
	}
	if !firewallManager.rollbackCall {
		t.Fatal("配置保存失败时应回滚本次新建的防火墙规则")
	}
}

func TestSetupSaveFailureKeepsExistingFirewallRule(t *testing.T) {
	baseDir := t.TempDir()
	blockedParent := filepath.Join(baseDir, "blocked")
	if err := os.WriteFile(blockedParent, []byte("占位"), 0o600); err != nil {
		t.Fatalf("创建阻塞路径失败：%v", err)
	}
	firewallManager := &fakeFirewallManager{result: firewall.Result{
		Backend: "ufw", RuleName: "Scert Web TCP 8443", Port: 8443, Created: false,
	}}
	server := New(Dependencies{
		Config: config.Default(baseDir), ConfigPath: filepath.Join(blockedParent, "scert.json"),
		Repository: &stubDomainRepository{}, SetupDNS: &fakeSetupDNSVerifier{}, Firewall: firewallManager,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(setupPayload(baseDir, "secure-pass-123")))
	request.RemoteAddr = "127.0.0.1:53000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("配置保存失败应返回服务器错误：%d %s", response.Code, response.Body.String())
	}
	if firewallManager.rollbackCall {
		t.Fatal("配置保存失败时不得删除初始化前已经存在的防火墙规则")
	}
}

func TestSetupFirewallFailureDoesNotSaveOrConsumeToken(t *testing.T) {
	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, "config", "scert.json")
	verifier := &fakeSetupDNSVerifier{}
	firewallManager := &fakeFirewallManager{openErr: errors.New("access denied")}
	server := New(Dependencies{
		Config: config.Default(baseDir), ConfigPath: configPath,
		Repository: &stubDomainRepository{}, SetupDNS: verifier, Firewall: firewallManager,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(setupPayload(baseDir, "secure-pass-123")))
	request.RemoteAddr = "127.0.0.1:53000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "管理员权限") {
		t.Fatalf("防火墙配置失败应返回明确错误：%d %s", response.Code, response.Body.String())
	}
	if verifier.consumed {
		t.Fatal("防火墙配置失败时不得消费 DNS 验证令牌")
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("防火墙配置失败时不得保存初始化配置：%v", err)
	}
}

func setupPayload(baseDir, password string) string {
	return `{
		"admin_username":"root-admin",
		"admin_email":"admin@example.test",
		"admin_password":"` + password + `",
		"server_port":8443,
		"dns_provider":"cloudflare",
		"dns_credential_ref":"secret://dns/cloudflare/test",
		"dns_verification_token":"verified-token",
		"dns_test_domain":"scert.example.test",
		"certificate_root":"` + filepath.ToSlash(filepath.Join(baseDir, "ssl")) + `",
		"web_hostname":"scert.example.test",
		"web_enabled":true,
		"https_mode":"direct"
	}`
}
