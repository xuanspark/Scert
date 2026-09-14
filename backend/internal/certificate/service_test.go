package certificate

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-acme/lego/v5/challenge"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

type serviceRepository struct {
	item      domain.Domain
	states    []string
	updateErr error
}

func (r *serviceRepository) GetByID(_ context.Context, _ string) (domain.Domain, error) {
	return r.item, nil
}

func (r *serviceRepository) UpdateCertificateState(_ context.Context, _ string, status string, expiresAt *time.Time) error {
	if status == "valid" && r.updateErr != nil {
		return r.updateErr
	}
	r.states = append(r.states, status)
	r.item.Status = status
	r.item.ExpiresAt = expiresAt
	return nil
}

type serviceCredentials struct{}

func (serviceCredentials) Get(context.Context, string) (map[string]string, error) {
	return map[string]string{"api_token": "test-token"}, nil
}

type serviceProvider struct{}

func (serviceProvider) Present(context.Context, string, string, string) error { return nil }
func (serviceProvider) CleanUp(context.Context, string, string, string) error { return nil }

type serviceProviderFactory struct{}

func (serviceProviderFactory) Create(string, map[string]string) (challenge.Provider, error) {
	return serviceProvider{}, nil
}

type serviceAuthorizationChecker struct {
	verifyCalls     int
	invalidateCalls int
}

func (c *serviceAuthorizationChecker) VerifyDomain(context.Context, string, string, map[string]string) error {
	c.verifyCalls++
	return nil
}

func (c *serviceAuthorizationChecker) InvalidateDomain(string, string, map[string]string) {
	c.invalidateCalls++
}

type recordingProviderFactory struct {
	providerID  string
	credentials map[string]string
}

func (f *recordingProviderFactory) Create(providerID string, credentials map[string]string) (challenge.Provider, error) {
	f.providerID = providerID
	f.credentials = credentials
	return serviceProvider{}, nil
}

type serviceAccounts struct{}

func (serviceAccounts) LoadOrCreate(context.Context, string, string) (*AccountUser, error) {
	return &AccountUser{}, nil
}

func (serviceAccounts) Save(context.Context, string, *AccountUser) error { return nil }

type serviceIssuer struct {
	request  ObtainRequest
	resource Resource
	err      error
}

func (i *serviceIssuer) Obtain(_ context.Context, request ObtainRequest) (Resource, error) {
	i.request = request
	return i.resource, i.err
}

func TestServiceIssuesSANCertificateAndUpdatesState(t *testing.T) {
	root := t.TempDir()
	cfg := certificateTestConfig(root)
	item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSAN, []string{"example.com", "www.example.com"}, true, domain.ACMEEnvironmentStaging, "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	repository := &serviceRepository{item: item}
	issuer := &serviceIssuer{resource: testResourceForDomains(t, []string{"example.com", "www.example.com"})}
	service := NewService(cfg, repository, serviceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
	service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer { return issuer }

	result, err := service.Issue(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("申请 SAN 证书失败：%v", err)
	}
	if len(issuer.request.Domains) != 2 {
		t.Fatalf("SAN 申请域名错误：%v", issuer.request.Domains)
	}
	if len(repository.states) != 2 || repository.states[0] != "issuing" || repository.states[1] != "valid" {
		t.Fatalf("证书状态流转错误：%v", repository.states)
	}
	if _, err := os.Stat(result.Saved.PrivateKeyPath); err != nil {
		t.Fatalf("证书私钥未保存：%v", err)
	}
}

func TestServiceFailureKeepsExistingCertificate(t *testing.T) {
	root := t.TempDir()
	cfg := certificateTestConfig(root)
	item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSingle, nil, true, domain.ACMEEnvironmentStaging, "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	oldExpiry := time.Now().UTC().Add(10 * 24 * time.Hour)
	item.ExpiresAt = &oldExpiry
	store := FileStore{Root: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup}
	oldResource := testResource(t, "example.com")
	saved, err := store.Save(context.Background(), item.Name, oldResource)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(saved.CertificatePath)
	if err != nil {
		t.Fatal(err)
	}
	repository := &serviceRepository{item: item}
	issuer := &serviceIssuer{err: errors.New("模拟 ACME 申请失败")}
	service := NewService(cfg, repository, serviceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
	service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer { return issuer }

	if _, err := service.Issue(context.Background(), item.ID); err == nil {
		t.Fatal("ACME 申请失败时应返回错误")
	}
	after, err := os.ReadFile(saved.CertificatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("申请失败时不应覆盖现有证书")
	}
	if len(repository.states) != 2 || repository.states[1] != "failed" {
		t.Fatalf("失败状态未正确记录：%v", repository.states)
	}
	if repository.item.ExpiresAt == nil || !repository.item.ExpiresAt.Equal(oldExpiry) {
		t.Fatal("申请失败时应保留原证书到期时间")
	}
}

func TestServiceInvalidatesDNSAuthorizationAfterACMEFailure(t *testing.T) {
	root := t.TempDir()
	cfg := certificateTestConfig(root)
	item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSingle, nil, true, domain.ACMEEnvironmentStaging, "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	repository := &serviceRepository{item: item}
	issuer := &serviceIssuer{err: errors.New("模拟 DNS 权限已被收回")}
	authorization := &serviceAuthorizationChecker{}
	service := NewService(cfg, repository, serviceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
	service.SetDNSAuthorizationChecker(authorization)
	service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer { return issuer }

	if _, err := service.Issue(context.Background(), item.ID); err == nil {
		t.Fatal("ACME 申请失败时应返回错误")
	}
	if authorization.verifyCalls != 1 || authorization.invalidateCalls != 1 {
		t.Fatalf("ACME 失败后的授权缓存处理错误：verify=%d invalidate=%d", authorization.verifyCalls, authorization.invalidateCalls)
	}
}

func TestServiceDoesNotInvalidateDNSAuthorizationForUnrelatedACMEFailure(t *testing.T) {
	root := t.TempDir()
	cfg := certificateTestConfig(root)
	item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSingle, nil, true, domain.ACMEEnvironmentStaging, "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	repository := &serviceRepository{item: item}
	issuer := &serviceIssuer{err: errors.New("ACME 服务暂时不可用")}
	authorization := &serviceAuthorizationChecker{}
	service := NewService(cfg, repository, serviceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
	service.SetDNSAuthorizationChecker(authorization)
	service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer { return issuer }

	if _, err := service.Issue(context.Background(), item.ID); err == nil {
		t.Fatal("ACME 申请失败时应返回错误")
	}
	if authorization.invalidateCalls != 0 {
		t.Fatalf("无关的 ACME 错误不应清除授权缓存：invalidate=%d", authorization.invalidateCalls)
	}
}

func TestServiceImportsOwnedCertificate(t *testing.T) {
	root := t.TempDir()
	cfg := certificateTestConfig(root)
	item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSAN, []string{"example.com", "api.example.com"}, true, domain.ACMEEnvironmentStaging, "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	resource := testResourceForDomains(t, []string{"example.com", "api.example.com"})
	certificatePath := filepath.Join(root, "owned-cert.pem")
	privateKeyPath := filepath.Join(root, "owned-key.pem")
	if err := os.WriteFile(certificatePath, resource.Certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKeyPath, resource.PrivateKey, 0o600); err != nil {
		t.Fatal(err)
	}
	repository := &serviceRepository{item: item}
	service := NewService(cfg, repository, nil, nil, nil)
	result, err := service.Import(context.Background(), ImportRequest{
		DomainID: item.ID, Certificate: certificatePath, PrivateKey: privateKeyPath,
	})
	if err != nil {
		t.Fatalf("导入自有证书失败：%v", err)
	}
	if len(result.Domains) != 2 || repository.item.Status != "valid" {
		t.Fatalf("导入结果错误：%+v，状态：%s", result, repository.item.Status)
	}
	if _, err := os.Stat(result.Saved.FullChainPath); err != nil {
		t.Fatalf("导入证书未保存：%v", err)
	}
}

func TestServiceRejectsOwnedCertificateForWrongDomain(t *testing.T) {
	root := t.TempDir()
	cfg := certificateTestConfig(root)
	item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSingle, nil, true, domain.ACMEEnvironmentStaging, "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	resource := testResource(t, "other.example.com")
	certificatePath := filepath.Join(root, "wrong-cert.pem")
	privateKeyPath := filepath.Join(root, "wrong-key.pem")
	if err := os.WriteFile(certificatePath, resource.Certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKeyPath, resource.PrivateKey, 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewService(cfg, &serviceRepository{item: item}, nil, nil, nil)
	if _, err := service.Import(context.Background(), ImportRequest{
		DomainID: item.ID, Certificate: certificatePath, PrivateKey: privateKeyPath,
	}); err == nil {
		t.Fatal("证书不覆盖托管域名时应拒绝导入")
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.Certificate, "example.com", "cert.pem")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("导入失败时不应写入证书：%v", err)
	}
}

func TestServiceRestoreVersionRollsBackFilesWhenStateUpdateFails(t *testing.T) {
	root := t.TempDir()
	cfg := certificateTestConfig(root)
	item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSingle, nil, true, domain.ACMEEnvironmentStaging, "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	store := FileStore{Root: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup}
	first, err := store.SaveForType(context.Background(), item.Name, "single", testResource(t, "example.com"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SaveForType(context.Background(), item.Name, "single", testResource(t, "example.com"))
	if err != nil {
		t.Fatal(err)
	}
	beforeCertificate, err := os.ReadFile(second.CertificatePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeKey, err := os.ReadFile(second.PrivateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	repository := &serviceRepository{item: item, updateErr: errors.New("simulated state update failure")}
	service := NewService(cfg, repository, nil, nil, nil)
	if _, err := service.RestoreVersion(context.Background(), item.ID, first.Version); err == nil {
		t.Fatal("state update failure should be returned")
	}
	afterCertificate, err := os.ReadFile(second.CertificatePath)
	if err != nil {
		t.Fatal(err)
	}
	afterKey, err := os.ReadFile(second.PrivateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterCertificate) != string(beforeCertificate) || string(afterKey) != string(beforeKey) {
		t.Fatal("certificate files were not rolled back after state update failure")
	}
	current, _, err := store.LoadCurrent(context.Background(), item.Name, string(item.CertificateType))
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != second.Version {
		t.Fatalf("current metadata was not rolled back: got=%s want=%s", current.Version, second.Version)
	}
}

func certificateTestConfig(root string) config.Config {
	cfg := config.Default(root)
	cfg.Paths.Certificate = filepath.Join(root, "ssl")
	cfg.Paths.Backup = filepath.Join(root, "backups")
	cfg.DNS.Provider = "cloudflare"
	cfg.DNS.CredentialRef = "secret://dns/cloudflare/test"
	cfg.DNS.VerificationState = "verified"
	cfg.Notification.Recipients = []string{"admin@example.com"}
	return cfg
}

func testResourceForDomains(t *testing.T, names []string) Resource {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(90 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return Resource{
		Domains: names, Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	}
}

func TestServiceIssuesSetupCertificateWithTypedLayout(t *testing.T) {
	root := t.TempDir()
	cfg := certificateTestConfig(root)
	issuer := &serviceIssuer{resource: testResource(t, "scert.example.com")}
	service := NewService(cfg, nil, serviceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
	service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer { return issuer }

	saved, err := service.IssueForSetup(context.Background(), SetupCertificateRequest{
		Domains: []string{"scert.example.com"}, CertificateType: "single",
		ProviderID: "cloudflare", CredentialRef: "secret://dns/cloudflare/test",
		AccountEmail: "admin@example.com", Environment: "staging",
		CertificateRoot: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup,
	})
	if err != nil {
		t.Fatalf("申请初始化证书失败：%v", err)
	}
	wantDirectory := filepath.Join(cfg.Paths.Certificate, "scert.example.com", "single", saved.Version)
	if saved.Directory != wantDirectory {
		t.Fatalf("初始化证书目录错误：got=%q want=%q", saved.Directory, wantDirectory)
	}
	if issuer.request.AccountEmail != "admin@example.com" || len(issuer.request.Domains) != 1 {
		t.Fatalf("初始化证书申请参数错误：%+v", issuer.request)
	}
	for _, path := range []string{saved.CertificatePath, saved.FullChainPath, saved.PrivateKeyPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("初始化证书文件不存在：%s：%v", path, err)
		}
	}
}

func TestServiceDecryptsCredentialReferenceBeforeCreatingProvider(t *testing.T) {
	root := t.TempDir()
	cfg := certificateTestConfig(root)
	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	credentialRef := "secret://dns/cloudflare/service-test"
	if err := store.Put(context.Background(), credentialRef, map[string]string{
		"api_token": "test-dns-token",
	}); err != nil {
		t.Fatalf("保存测试 DNS 凭据失败：%v", err)
	}
	cfg.DNS.CredentialRef = credentialRef
	item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSingle, nil, true, domain.ACMEEnvironmentStaging, "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	repository := &serviceRepository{item: item}
	factory := &recordingProviderFactory{}
	issuer := &serviceIssuer{resource: testResource(t, "example.com")}
	service := NewService(cfg, repository, store, factory, serviceAccounts{})
	service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer { return issuer }

	if _, err := service.Issue(context.Background(), item.ID); err != nil {
		t.Fatalf("使用加密凭据引用申请证书失败：%v", err)
	}
	if factory.providerID != "cloudflare" || factory.credentials["api_token"] != "test-dns-token" {
		t.Fatalf("创建 DNS Provider 时未正确解密凭据：provider=%q credentials=%v", factory.providerID, factory.credentials)
	}
}

func TestNewServiceInjectsPropagationSettings(t *testing.T) {
	cfg := certificateTestConfig(t.TempDir())
	cfg.ACME.PropagationTimeoutSecs = 1200
	cfg.ACME.PropagationPollingIntervalSecs = 15
	service := NewService(cfg, nil, nil, nil, nil)
	issuer, ok := service.newIssuer(&AccountUser{}, serviceProvider{}, "staging").(*LegoIssuer)
	if !ok {
		t.Fatal("创建的 Issuer 类型不正确")
	}
	if issuer.PropagationTimeout != 1200*time.Second || issuer.PropagationInterval != 15*time.Second {
		t.Fatalf("DNS 传播参数注入不正确：timeout=%s interval=%s", issuer.PropagationTimeout, issuer.PropagationInterval)
	}
}
