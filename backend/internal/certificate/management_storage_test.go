package certificate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-acme/lego/v5/challenge"
)

func TestManagementCertificatePreservesReferencedVersions(t *testing.T) {
	cfg := certificateTestConfig(t.TempDir())
	issuer := &serviceIssuer{resource: testResource(t, "management.example.com")}
	service := NewService(cfg, nil, serviceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
	service.acmeRoots = testResourceRoots(t, issuer.resource)
	service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer { return issuer }
	request := SetupCertificateRequest{
		Domains: []string{"management.example.com"}, CertificateType: "single",
		ProviderID: "cloudflare", CredentialRef: "secret://dns/cloudflare/test",
		AccountEmail: "admin@example.com", Environment: "staging",
		CertificateRoot: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup,
	}
	first, err := service.IssueForSetup(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	original := make(map[string][]byte)
	for _, name := range []string{"cert.pem", "fullchain.pem", "key.pem", "metadata.json"} {
		data, err := os.ReadFile(filepath.Join(first.Directory, name))
		if err != nil {
			t.Fatal(err)
		}
		original[name] = data
	}
	// 使用不同证书证明后续申请及失败回退不会覆盖旧版本内容。
	issuer.resource = testResource(t, "management.example.com")
	service.acmeRoots = testResourceRoots(t, issuer.resource)
	second, err := service.IssueForSetup(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Directory == first.Directory || second.BackupDirectory == "" {
		t.Fatal("管理证书候选必须保存到新版本并备份旧版本")
	}
	assertUnchanged := func() {
		t.Helper()
		for name, expected := range original {
			actual, err := os.ReadFile(filepath.Join(first.Directory, name))
			if err != nil || !bytes.Equal(actual, expected) {
				t.Fatalf("仍被引用的管理证书文件被修改：%s %v", name, err)
			}
		}
	}
	assertUnchanged()
	if err := RollbackSavedResource(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	assertUnchanged()
	third, err := service.IssueForSetup(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if third.Directory == second.Directory || third.Directory == first.Directory {
		t.Fatal("后续申请不得复用既有管理证书版本")
	}
	assertUnchanged()
	if _, err := os.Stat(second.FullChainPath); err != nil {
		t.Fatalf("失败候选应保留用于审查：%v", err)
	}
}
