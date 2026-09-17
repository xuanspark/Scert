package certificate

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-acme/lego/v5/challenge"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func TestManagementStorageIsolatedFromBusinessCertificates(t *testing.T) {
	for _, certificateType := range []domain.CertificateType{domain.CertificateSingle, domain.CertificateWildcard, domain.CertificateSAN} {
		t.Run(string(certificateType), func(t *testing.T) {
			cfg := certificateTestConfig(t.TempDir())
			name := "management.example.com"
			if certificateType == domain.CertificateWildcard {
				name = "*.example.com"
			}
			item, err := domain.NewDomainWithOptions(name, certificateType, []string{"www.example.com"}, true, "staging", "cloudflare")
			if err != nil {
				t.Fatal(err)
			}
			names, err := requestedDomains(item)
			if err != nil {
				t.Fatal(err)
			}
			issuer := &serviceIssuer{resource: testResourceForDomains(t, names)}
			service := NewService(cfg, &serviceRepository{item: item}, serviceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
			service.acmeRoots = testResourceRoots(t, issuer.resource)
			service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer { return issuer }
			request := SetupCertificateRequest{
				Domains: names, CertificateType: string(certificateType),
				ProviderID: "cloudflare", CredentialRef: cfg.DNS.CredentialRef,
				Environment: "staging", CertificateRoot: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup,
			}
			management, err := service.IssueForSetup(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if !pathWithinDirectory(filepath.Join(cfg.Paths.Certificate, "management-domain"), management.Directory) {
				t.Fatalf("管理证书未进入独立目录：%s", management.Directory)
			}
			original := snapshotCertificateFiles(t, management)
			business, err := service.Issue(t.Context(), item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if pathWithinDirectory(filepath.Join(cfg.Paths.Certificate, "management-domain"), business.Saved.Directory) {
				t.Fatal("业务证书进入了管理证书目录")
			}
			businessOriginal := snapshotCertificateFiles(t, business.Saved)
			renewed, err := service.IssueForSetup(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if !pathWithinDirectory(filepath.Join(cfg.Paths.Backup, "management-domain"), renewed.BackupDirectory) {
				t.Fatalf("管理证书备份未隔离：%s", renewed.BackupDirectory)
			}
			assertCertificateFilesUnchanged(t, businessOriginal)
			issuer.resource = testResourceForDomains(t, names)
			service.acmeRoots = testResourceRoots(t, issuer.resource)
			if _, err := service.Issue(t.Context(), item.ID); err != nil {
				t.Fatal(err)
			}
			assertCertificateFilesUnchanged(t, original)
			if _, err := os.Stat(business.Saved.Directory); !os.IsNotExist(err) {
				t.Fatalf("普通业务仍应清理旧当前版本：%v", err)
			}
			current, err := service.Current(t.Context(), item.ID)
			if err != nil || current.Saved.Directory == management.Directory || current.Saved.Directory == renewed.Directory {
				t.Fatalf("业务当前证书读取越界：%+v %v", current.Saved, err)
			}
		})
	}
}

func TestManagementLegacyReferenceSurvivesBusinessIssueAndImport(t *testing.T) {
	for _, operation := range []string{"issue", "import"} {
		t.Run(operation, func(t *testing.T) {
			cfg := certificateTestConfig(t.TempDir())
			item, err := domain.NewDomain("management.example.com", domain.CertificateSingle, nil)
			if err != nil {
				t.Fatal(err)
			}
			legacyStore := FileStore{Root: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup}
			legacy, err := legacyStore.SaveForType(t.Context(), item.Name, "single", testResource(t, item.Name))
			if err != nil {
				t.Fatal(err)
			}
			original := snapshotCertificateFiles(t, legacy)
			// 分别覆盖仅通过证书或私钥路径保护旧版本的情况。
			if operation == "issue" {
				cfg.Server.TLSCert = legacy.FullChainPath
			} else {
				cfg.Server.TLSKey = legacy.PrivateKeyPath
			}
			issuer := &serviceIssuer{resource: testResource(t, item.Name)}
			inputDirectory := t.TempDir()
			if err := writeCertificateFiles(inputDirectory, issuer.resource.Certificate, issuer.resource.Certificate, issuer.resource.PrivateKey); err != nil {
				t.Fatal(err)
			}
			service := NewService(cfg, &serviceRepository{item: item}, serviceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
			service.acmeRoots = testResourceRoots(t, issuer.resource)
			service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer { return issuer }
			var first SavedResource
			for index := 0; index < 2; index++ {
				var result IssueResult
				if operation == "issue" {
					result, err = service.Issue(t.Context(), item.ID)
				} else {
					result, err = service.Import(t.Context(), ImportRequest{
						DomainID: item.ID, Certificate: filepath.Join(inputDirectory, "cert.pem"), PrivateKey: filepath.Join(inputDirectory, "key.pem"),
					})
				}
				if err != nil {
					t.Fatal(err)
				}
				if index == 0 {
					first = result.Saved
				}
				assertCertificateFilesUnchanged(t, original)
			}
			if _, err := os.Stat(first.Directory); !os.IsNotExist(err) {
				t.Fatalf("保护管理入口不应保留无引用的业务旧版本：%v", err)
			}
		})
	}
}

func TestManagementStorageRejectsInvalidRootsBeforeExternalCalls(t *testing.T) {
	for _, roots := range [][2]string{{"", ""}, {"relative", t.TempDir()}, {t.TempDir(), "relative"}} {
		cfg := certificateTestConfig(t.TempDir())
		credentials := &protectedDomainCredentials{}
		providers := &protectedDomainProviderFactory{}
		accounts := &protectedDomainAccounts{}
		service := NewService(cfg, nil, credentials, providers, accounts)
		_, err := service.IssueForSetup(t.Context(), SetupCertificateRequest{
			Domains: []string{"management.example.com"}, CertificateType: "single",
			ProviderID: "cloudflare", CredentialRef: cfg.DNS.CredentialRef,
			CertificateRoot: roots[0], BackupRoot: roots[1],
		})
		if err == nil || credentials.getCalls != 0 || providers.createCalls != 0 || accounts.loadOrCreateCalls != 0 {
			t.Fatalf("无效目录必须在外部调用之前被拒绝：%v", err)
		}
	}
}

func TestManagementProtectedPathsResolveSymlinks(t *testing.T) {
	root := t.TempDir()
	version := filepath.Join(root, "20260918-V1.0")
	if err := os.Mkdir(version, 0o700); err != nil {
		t.Fatal(err)
	}
	referenced := filepath.Join(version, "key.pem")
	if err := os.WriteFile(referenced, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "current-key.pem")
	if err := os.Symlink(referenced, alias); err != nil {
		t.Skipf("当前系统无法创建测试符号链接：%v", err)
	}
	store := FileStore{ProtectedPaths: []string{alias}}
	if protected, err := store.isProtectedDirectory(version); err != nil || !protected {
		t.Fatalf("符号链接引用的管理私钥必须受保护：%v %v", protected, err)
	}
}

func TestManagementProtectedPathsDoNotMatchSiblingPrefix(t *testing.T) {
	root := t.TempDir()
	store := FileStore{ProtectedPaths: []string{filepath.Join(root+"-other", "key.pem")}}
	if protected, err := store.isProtectedDirectory(root); err != nil || protected {
		t.Fatalf("路径前缀相同不代表目录包含：%v %v", protected, err)
	}
}

func TestManagementUnresolvableReferenceDoesNotWriteCandidate(t *testing.T) {
	cfg := certificateTestConfig(t.TempDir())
	store := FileStore{Root: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup}
	resource := testResource(t, "management.example.com")
	first, err := store.SaveForType(t.Context(), "management.example.com", "single", resource)
	if err != nil {
		t.Fatal(err)
	}
	original := snapshotCertificateFiles(t, first)
	alias := filepath.Join(t.TempDir(), "invalid-link")
	if err := os.Symlink(alias, alias); err != nil {
		t.Skipf("当前系统无法创建测试符号链接：%v", err)
	}
	store.ProtectedPaths = []string{alias}
	if _, err := store.SaveForType(t.Context(), "management.example.com", "single", resource); err == nil {
		t.Fatal("无法解析管理证书引用时必须停止写入")
	}
	assertCertificateFilesUnchanged(t, original)
	entries, err := os.ReadDir(filepath.Dir(first.Directory))
	if err != nil || len(entries) != 1 {
		t.Fatalf("引用检查失败不应产生候选版本：%d %v", len(entries), err)
	}
}

func snapshotCertificateFiles(t *testing.T, saved SavedResource) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	for _, name := range []string{"cert.pem", "fullchain.pem", "key.pem", "metadata.json"} {
		path := filepath.Join(saved.Directory, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		result[path] = data
	}
	return result
}

func assertCertificateFilesUnchanged(t *testing.T, expected map[string][]byte) {
	t.Helper()
	for path, original := range expected {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, actual) {
			t.Fatalf("仍被引用的证书文件发生变化：%s %v", path, err)
		}
	}
}
