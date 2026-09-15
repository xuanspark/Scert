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
			service := NewService(cfg, repo, serviceCredentials{}, provider, serviceAccounts{})
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
	service := NewService(cfg, nil, serviceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
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
