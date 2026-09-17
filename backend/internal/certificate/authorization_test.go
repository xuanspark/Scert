package certificate

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/go-acme/lego/v5/challenge"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type scopedAuthorizationChecker struct {
	denied string
	calls  []string
}

type scopedServiceCredentials struct{}

func (scopedServiceCredentials) Get(context.Context, string) (map[string]string, error) {
	return map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret"}, nil
}

func (c *scopedAuthorizationChecker) VerifyDomain(_ context.Context, _ string, name string, _ map[string]string) error {
	c.calls = append(c.calls, name)
	if name == c.denied {
		return errors.New("Forbidden.NoPermission")
	}
	return nil
}

func TestServiceRejectsUnauthorizedSANBeforeIssuerOrFileWrites(t *testing.T) {
	for _, denied := range []string{"example.com", "other.example.org"} {
		t.Run(denied, func(t *testing.T) {
			cfg := certificateTestConfig(t.TempDir())
			cfg.DNS.Provider = "aliyun"
			item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSAN, []string{"other.example.org"}, false, "staging", "aliyun")
			if err != nil {
				t.Fatal(err)
			}
			provider := &recordingProviderFactory{}
			repo := &serviceRepository{item: item}
			checker := &scopedAuthorizationChecker{denied: denied}
			service := NewService(cfg, repo, scopedServiceCredentials{}, provider, serviceAccounts{})
			service.SetDNSAuthorizationChecker(checker)
			service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer {
				t.Fatal("任意 SAN 权限拒绝后不得创建 ACME 签发器")
				return nil
			}
			_, err = service.Issue(t.Context(), item.ID)
			if err == nil || !strings.Contains(err.Error(), denied) || !strings.Contains(err.Error(), "Forbidden.NoPermission") {
				t.Fatalf("必须拒绝并指出无权限的 SAN：%v", err)
			}
			if provider.providerID != "" {
				t.Fatal("权限拒绝后不得创建 DNS 挑战 Provider")
			}
			if repo.item.Status != "failed" {
				t.Fatalf("失败状态未保存：%s", repo.item.Status)
			}
			if _, err := os.Stat(cfg.Paths.Certificate); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("权限拒绝后不得写入证书文件")
			}
		})
	}
}

func TestSetupRejectsUnauthorizedDomainBeforeIssuer(t *testing.T) {
	cfg := certificateTestConfig(t.TempDir())
	service := NewService(cfg, nil, scopedServiceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
	service.SetDNSAuthorizationChecker(&scopedAuthorizationChecker{denied: "other.example.org"})
	service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer {
		t.Fatal("初始化证书中的未授权域名必须在 ACME 请求前拒绝")
		return nil
	}
	_, err := service.IssueForSetup(t.Context(), SetupCertificateRequest{
		Domains: []string{"example.com", "other.example.org"}, CertificateType: "san",
		ProviderID: "aliyun", CredentialRef: "secret://dns/test", Environment: "staging",
		CertificateRoot: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup,
	})
	if err == nil || !strings.Contains(err.Error(), "other.example.org") {
		t.Fatalf("初始化证书申请未拒绝未授权域名：%v", err)
	}
}

func TestAliyunMissingAuthorizationCheckerBlocksIssueAndSetup(t *testing.T) {
	cfg := certificateTestConfig(t.TempDir())
	cfg.DNS.Provider = "aliyun"
	item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSingle, nil, false, "staging", "aliyun")
	if err != nil {
		t.Fatal(err)
	}
	provider := &recordingProviderFactory{}
	service := NewService(cfg, &serviceRepository{item: item}, scopedServiceCredentials{}, provider, serviceAccounts{})
	if _, err := service.Issue(t.Context(), item.ID); err == nil || !strings.Contains(err.Error(), "探测器未初始化") {
		t.Fatalf("缺失检查器必须阻止签发：%v", err)
	}
	if _, err := service.IssueForSetup(t.Context(), SetupCertificateRequest{
		Domains: []string{"example.com"}, CertificateType: "single", ProviderID: "aliyun",
		CredentialRef: cfg.DNS.CredentialRef, Environment: "staging",
		CertificateRoot: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup,
	}); err == nil || !strings.Contains(err.Error(), "探测器未初始化") {
		t.Fatalf("缺失检查器必须阻止初始化证书签发：%v", err)
	}
	if provider.providerID != "" {
		t.Fatal("缺失检查器时不得创建 DNS 挑战 Provider")
	}
}

type protectedDomainCredentials struct {
	getCalls int
}

func (c *protectedDomainCredentials) Get(context.Context, string) (map[string]string, error) {
	c.getCalls++
	return map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret"}, nil
}

type protectedDomainProviderFactory struct {
	createCalls int
}

func (f *protectedDomainProviderFactory) Create(string, map[string]string) (challenge.Provider, error) {
	f.createCalls++
	return serviceProvider{}, nil
}

type protectedDomainAccounts struct {
	loadOrCreateCalls int
	saveCalls         int
}

func (a *protectedDomainAccounts) LoadOrCreate(context.Context, string, string) (*AccountUser, error) {
	a.loadOrCreateCalls++
	return &AccountUser{}, nil
}

func (a *protectedDomainAccounts) Save(context.Context, string, *AccountUser) error {
	a.saveCalls++
	return nil
}

func TestAliyunProtectedSANRejectedBeforeAnyExternalSideEffect(t *testing.T) {
	cfg := certificateTestConfig(t.TempDir())
	credentials := &protectedDomainCredentials{}
	providerFactory := &protectedDomainProviderFactory{}
	accounts := &protectedDomainAccounts{}
	checker := &scopedAuthorizationChecker{}
	service := NewService(cfg, nil, credentials, providerFactory, accounts)
	service.SetDNSAuthorizationChecker(checker)
	issuerFactoryCalls := 0
	service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer {
		issuerFactoryCalls++
		return nil
	}
	_, err := service.IssueForSetup(t.Context(), SetupCertificateRequest{
		Domains: []string{"example.com", "pcux.cn"}, CertificateType: "san",
		ProviderID: "aliyun", CredentialRef: "secret://dns/test", Environment: "staging",
	})
	if err == nil || !strings.Contains(err.Error(), "pcux.cn") {
		t.Fatalf("保护 SAN 必须被本地拒绝：%v", err)
	}
	if credentials.getCalls != 0 {
		t.Fatalf("保护 SAN 被拒绝前不得读取 DNS 凭据，调用次数 = %d", credentials.getCalls)
	}
	if len(checker.calls) != 0 {
		t.Fatalf("保护 SAN 被拒绝前不得执行 DNS 权限探测：%v", checker.calls)
	}
	if providerFactory.createCalls != 0 {
		t.Fatalf("保护 SAN 被拒绝前不得创建 DNS Provider，调用次数 = %d", providerFactory.createCalls)
	}
	if accounts.loadOrCreateCalls != 0 || accounts.saveCalls != 0 {
		t.Fatalf("保护 SAN 被拒绝前不得访问 ACME 账户：LoadOrCreate=%d Save=%d", accounts.loadOrCreateCalls, accounts.saveCalls)
	}
	if issuerFactoryCalls != 0 {
		t.Fatalf("保护 SAN 被拒绝前不得创建 ACME 签发器，调用次数 = %d", issuerFactoryCalls)
	}
}
