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
	allowedZones      []string
	verifyCalls       int
	verifyDomainCalls int
}

func (f *fakeAliyunScopeChecker) Verify(ctx context.Context, requestedDomain string, credentials map[string]string) (dnsprovider.AuthorizationReport, error) {
	f.verifyCalls++
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
	f.verifyDomainCalls++
	for _, zone := range f.allowedZones {
		if requestedDomain == zone || strings.HasSuffix(requestedDomain, "."+zone) {
			return nil
		}
	}
	return errors.New("SDKError: Code: Forbidden.NoPermission, Message: Specified access key is not authorized.")
}

type fakeSecretReaderStore struct {
	data     map[string]map[string]string
	getCalls int
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
	s.getCalls++
	if val, ok := s.data[ref]; ok {
		return val, nil
	}
	return nil, errors.New("secret not found")
}

func TestAliyunDomainCheckUsesOnlyCurrentTargetAndProtectsPCUX(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.SetupReady = true
	cfg.DNS.Provider = "aliyun"
	cfg.DNS.CredentialRef = "secret://dns/aliyun/ref1"

	store := &fakeSecretReaderStore{
		data: map[string]map[string]string{
			"secret://dns/aliyun/ref1": {
				"access_key_id":     "LTAI_test",
				"access_key_secret": "test_secret",
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

	// 旧的授权范围枚举接口已移除，不能重新引入账户域名发现能力。
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/dns/aliyun/scope", nil)
		serveAdminHTTP(server, w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("已移除的授权范围接口状态码 = %d，期望 404，响应：%s", w.Code, w.Body.String())
		}
	}

	// 对用户刚输入的域名使用当前 RAM 凭据执行验证，不依赖手工范围字段。
	{
		w := httptest.NewRecorder()
		body := strings.NewReader(`{"domain":"solit.cn"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/dns/aliyun/check", body)
		serveSuperAdminHTTP(server, w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST check solit.cn status = %d; body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"verified":true`) {
			t.Fatalf("solit.cn 应验证通过：%s", w.Body.String())
		}
		if store.getCalls != 1 || checker.verifyCalls != 1 {
			t.Fatalf("正常域名必须仅读取一次凭据并验证一次：读取=%d，验证=%d", store.getCalls, checker.verifyCalls)
		}
	}

	// pcux.cn 必须先本地拒绝，不能读取凭据或调用云端探测器。
	{
		w := httptest.NewRecorder()
		body := strings.NewReader(`{"domain":"pcux.cn"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/dns/aliyun/check", body)
		serveSuperAdminHTTP(server, w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST check pcux.cn 状态码 = %d，响应：%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"verified":false`) {
			t.Fatalf("pcux.cn 应被本地拒绝：%s", w.Body.String())
		}
		if store.getCalls != 1 || checker.verifyCalls != 1 || checker.verifyDomainCalls != 0 {
			t.Fatalf("保护域不能触发凭据读取或云端探测：读取=%d，检查=%d，域名检查=%d", store.getCalls, checker.verifyCalls, checker.verifyDomainCalls)
		}
	}

	if err := server.validateDomainAliyunAuthorization(t.Context(), "pcux.cn"); err == nil {
		t.Fatal("直接校验 pcux.cn 必须被拒绝")
	}
	if store.getCalls != 1 || checker.verifyDomainCalls != 0 {
		t.Fatalf("直接校验保护域不能读取凭据或调用探测：读取=%d，域名检查=%d", store.getCalls, checker.verifyDomainCalls)
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

func TestAliyunDomainAPIRejectsProtectedSANWithoutSideEffects(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.DNS.Provider = dnsprovider.Aliyun
	cfg.DNS.CredentialRef = "secret://dns/test"
	store := &fakeSecretReaderStore{data: map[string]map[string]string{
		cfg.DNS.CredentialRef: {"access_key_id": "test-id", "access_key_secret": "test-secret"},
	}}
	checker := &fakeAliyunScopeChecker{allowedZones: []string{"example.com"}}
	repo := &stubDomainRepository{}
	server := New(Dependencies{
		Config: cfg, Repository: repo, ProjectDomains: projectDomainRepositoryStub{DomainRepository: repo}, Secrets: store, AliyunChecker: checker,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"example.com","certificate_type":"san","sans":["pcux.cn"]}`))
	serveAdminHTTP(server, w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "pcux.cn") {
		t.Fatalf("包含保护 SAN 的整单应被拒绝：%d %s", w.Code, w.Body.String())
	}
	if repo.created.ID != "" || store.getCalls != 0 || checker.verifyDomainCalls != 0 {
		t.Fatalf("保护 SAN 被拒绝时不得持久化、读取凭据或探测：已创建=%t，读取=%d，检查=%d", repo.created.ID != "", store.getCalls, checker.verifyDomainCalls)
	}
}
