package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
)

type fakeAliyunScopeChecker struct {
	allowedZones []string
}

func (f *fakeAliyunScopeChecker) Discover(ctx context.Context, credentials map[string]string) (dnsprovider.AuthorizationReport, error) {
	// 模拟 RAM 账号处于最小资源限制模式，无全局枚举权限
	return dnsprovider.AuthorizationReport{}, errors.New("SDKError: Code: Forbidden.NoPermission")
}

func (f *fakeAliyunScopeChecker) Verify(ctx context.Context, requestedDomain string, credentials map[string]string) (dnsprovider.AuthorizationReport, error) {
	report := dnsprovider.AuthorizationReport{
		Provider:        dnsprovider.Aliyun,
		RequestedDomain: requestedDomain,
	}
	for _, zone := range f.allowedZones {
		if requestedDomain == zone || strings.HasSuffix(requestedDomain, "."+zone) {
			report.Verified = true
			report.MatchedDomain = zone
			return report, nil
		}
	}
	err := errors.New("SDKError: Code: Forbidden.NoPermission, Message: Specified access key is not authorized.")
	report.Diagnosis = dnsprovider.DiagnoseAliyunError(err, requestedDomain)
	return report, err
}

func (f *fakeAliyunScopeChecker) VerifyDomain(ctx context.Context, providerID, requestedDomain string, credentials map[string]string) error {
	for _, zone := range f.allowedZones {
		if requestedDomain == zone || strings.HasSuffix(requestedDomain, "."+zone) {
			return nil
		}
	}
	return errors.New("SDKError: Code: Forbidden.NoPermission, Message: Specified access key is not authorized.")
}

type fakeSecretReaderStore struct {
	data map[string]map[string]string
}

func (s *fakeSecretReaderStore) Put(ctx context.Context, ref string, val map[string]string) error {
	if s.data == nil {
		s.data = make(map[string]map[string]string)
	}
	s.data[ref] = val
	return nil
}

func (s *fakeSecretReaderStore) Delete(ctx context.Context, ref string) error {
	delete(s.data, ref)
	return nil
}

func (s *fakeSecretReaderStore) Get(ctx context.Context, ref string) (map[string]string, error) {
	if val, ok := s.data[ref]; ok {
		return val, nil
	}
	return nil, errors.New("secret not found")
}

func TestAliyunScopeAPIsAndDomainValidation(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.SetupReady = true
	cfg.DNS.Provider = "aliyun"
	cfg.DNS.CredentialRef = "secret://dns/aliyun/ref1"

	store := &fakeSecretReaderStore{
		data: map[string]map[string]string{
			"secret://dns/aliyun/ref1": {
				"access_key_id":     "LTAI_test",
				"access_key_secret": "test_secret",
				"managed_zones":     "solit.cn,unauth.com,forbidden.cn",
				"allowed_domains":   "solit.cn,*.solit.cn,unauth.com,forbidden.cn",
			},
		},
	}

	checker := &fakeAliyunScopeChecker{
		allowedZones: []string{"solit.cn", "odacm.cn", "apmmk.cn", "ifmj.uno", "vraq.uno"},
	}

	repo := &stubDomainRepository{}
	server := New(Dependencies{
		Config:        cfg,
		Repository:    repo,
		Secrets:       store,
		AliyunChecker: checker,
	})

	// 1. 测试 GET /api/v1/dns/aliyun/scope（受限模式报告）
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/dns/aliyun/scope", nil)
		serveAdminHTTP(server, w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/dns/aliyun/scope status = %d; want 200, body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "resource_scoped") {
			t.Fatalf("expected resource_scoped policy mode, got: %s", w.Body.String())
		}
	}

	// 2. 测试 POST /api/v1/dns/aliyun/check 验证授权域名（如 solit.cn）
	{
		w := httptest.NewRecorder()
		body := strings.NewReader(`{"domain":"solit.cn"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/dns/aliyun/check", body)
		serveAdminHTTP(server, w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST check solit.cn status = %d; body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"verified":true`) {
			t.Fatalf("solit.cn 应验证通过: %s", w.Body.String())
		}
	}

	// 3. 测试 POST /api/v1/dns/aliyun/check 验证未授权域名（如 unauth.com）
	{
		w := httptest.NewRecorder()
		body := strings.NewReader(`{"domain":"unauth.com"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/dns/aliyun/check", body)
		serveAdminHTTP(server, w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST check unauth.com status = %d; body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"verified":false`) {
			t.Fatalf("unauth.com 应验证失败: %s", w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "acs:alidns:*:*:domain/unauth.com") {
			t.Fatalf("应包含明确的 RAM 策略建议: %s", w.Body.String())
		}
	}

	// 4. 测试 POST /api/v1/domains 添加授权域名（例如 *.solit.cn）
	{
		w := httptest.NewRecorder()
		body := strings.NewReader(`{"name":"test.solit.cn","certificate_type":"single"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/domains", body)
		serveAdminHTTP(server, w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("创建授权域名应成功，got status = %d; body=%s", w.Code, w.Body.String())
		}
	}

	// 5. 测试 POST /api/v1/domains 添加未授权域名（例如 forbidden.cn），应被前置阻断
	{
		w := httptest.NewRecorder()
		body := strings.NewReader(`{"name":"forbidden.cn","certificate_type":"single"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/domains", body)
		serveAdminHTTP(server, w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("添加未授权域名应被 400 阻断，got status = %d; body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "acs:alidns:*:*:domain/forbidden.cn") {
			t.Fatalf("阻断提示应包含针对该域名的 RAM 授权指导，got: %s", w.Body.String())
		}
	}
}

func TestAliyunDomainAuthorizationFailsClosed(t *testing.T) {
	for _, scenario := range []string{"checker_missing", "reference_missing", "store_missing", "secret_missing", "secret_empty", "secret_incomplete", "setup_incomplete"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := readyTestConfig(t.TempDir())
			cfg.DNS.Provider = dnsprovider.Aliyun
			cfg.DNS.CredentialRef = "secret://dns/test"
			store := &fakeSecretReaderStore{data: map[string]map[string]string{
				cfg.DNS.CredentialRef: {"access_key_id": "test-id", "access_key_secret": "test-secret"},
			}}
			deps := Dependencies{Config: cfg, Secrets: store, AliyunChecker: &fakeAliyunScopeChecker{allowedZones: []string{"example.com"}}}
			switch scenario {
			case "checker_missing":
				deps.AliyunChecker = nil
			case "reference_missing":
				deps.Config.DNS.CredentialRef = ""
			case "store_missing":
				deps.Secrets = nil
			case "secret_missing":
				store.data = nil
			case "secret_empty":
				store.data[cfg.DNS.CredentialRef] = nil
			case "secret_incomplete":
				store.data[cfg.DNS.CredentialRef] = map[string]string{"access_key_id": "test-id"}
			case "setup_incomplete":
				deps.Config.SetupReady = false
			}
			server := New(deps)
			if err := server.validateDomainAliyunAuthorization(t.Context(), "example.com"); err == nil {
				t.Fatal("授权检查依赖异常必须拒绝，不能当作成功")
			}
		})
	}
}

func TestAliyunDomainAPIRejectsPartiallyUnauthorizedSAN(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.DNS.Provider = dnsprovider.Aliyun
	cfg.DNS.CredentialRef = "secret://dns/test"
	server := New(Dependencies{
		Config: cfg, Repository: &stubDomainRepository{},
		Secrets: &fakeSecretReaderStore{data: map[string]map[string]string{
			cfg.DNS.CredentialRef: {"access_key_id": "test-id", "access_key_secret": "test-secret", "managed_zones": "example.com", "allowed_domains": "example.com"},
		}},
		AliyunChecker: &fakeAliyunScopeChecker{allowedZones: []string{"example.com"}},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"example.com","certificate_type":"san","sans":["example.org"]}`))
	serveAdminHTTP(server, w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "example.org") {
		t.Fatalf("部分 SAN 未授权应整单拒绝：%d %s", w.Code, w.Body.String())
	}
}
