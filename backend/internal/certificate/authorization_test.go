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
	return map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret", "managed_zones": "example.com,example.org", "allowed_domains": "example.com,other.example.org"}, nil
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
	}); err == nil || !strings.Contains(err.Error(), "探测器未初始化") {
		t.Fatalf("缺失检查器必须阻止初始化证书签发：%v", err)
	}
	if provider.providerID != "" {
		t.Fatal("缺失检查器时不得创建 DNS 挑战 Provider")
	}
}

func TestAliyunWholeOrderScopeValidationBeforeAnyCloudProbe(t *testing.T) {
	cfg := certificateTestConfig(t.TempDir())
	checker := &scopedAuthorizationChecker{}
	service := NewService(cfg, nil, scopedServiceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
	service.SetDNSAuthorizationChecker(checker)
	_, err := service.IssueForSetup(t.Context(), SetupCertificateRequest{
		Domains: []string{"example.com", "outside.net"}, CertificateType: "san",
		ProviderID: "aliyun", CredentialRef: "secret://dns/test", Environment: "staging",
	})
	if err == nil || !strings.Contains(err.Error(), "outside.net") || len(checker.calls) != 0 {
		t.Fatalf("部分越界必须零云调用拒绝整单：%v %v", err, checker.calls)
	}
}
