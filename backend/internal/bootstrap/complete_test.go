package bootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsverification"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

type completionMemorySecrets struct {
	mu      sync.Mutex
	values  map[string]map[string]string
	deleted []string
}

func newCompletionMemorySecrets() *completionMemorySecrets {
	return &completionMemorySecrets{values: make(map[string]map[string]string)}
}

func (store *completionMemorySecrets) Put(_ context.Context, reference string, value map[string]string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.values[reference] = cloneCompletionSecrets(value)
	return nil
}

func (store *completionMemorySecrets) Get(_ context.Context, reference string) (map[string]string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.values[reference]
	if !ok {
		return nil, errors.New("secret not found")
	}
	return cloneCompletionSecrets(value), nil
}

func (store *completionMemorySecrets) Delete(_ context.Context, reference string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.values, reference)
	store.deleted = append(store.deleted, reference)
	return nil
}

func (store *completionMemorySecrets) has(reference string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, ok := store.values[reference]
	return ok
}

func cloneCompletionSecrets(value map[string]string) map[string]string {
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

type completionDNSTester struct{}

func (completionDNSTester) Verify(_ context.Context, _, testDomain string, _ map[string]string) (setup.DNSVerificationReport, error) {
	return dnsverification.Report{RecordName: "_scert." + testDomain}, nil
}

type completionCertificateIssuer struct {
	root  string
	saved certificate.SavedResource
	err   error
	calls int
}

func (issuer *completionCertificateIssuer) IssueForSetup(ctx context.Context, request certificate.SetupCertificateRequest) (certificate.SavedResource, error) {
	issuer.calls++
	if issuer.err != nil {
		return certificate.SavedResource{}, issuer.err
	}
	resource := completionDomainResource(request.Domains[0])
	store := certificate.FileStore{Root: issuer.root, BackupRoot: filepath.Join(filepath.Dir(issuer.root), "backups")}
	saved, err := store.SaveForType(ctx, request.Domains[0], "single", resource)
	issuer.saved = saved
	return saved, err
}

type completionFirewall struct {
	mu            sync.Mutex
	result        firewall.Result
	openErr       error
	rollbackErr   error
	openCalls     int
	rollbackCalls int
	block         chan struct{}
}

func (manager *completionFirewall) OpenTCP(_ context.Context, port int) (firewall.Result, error) {
	manager.mu.Lock()
	manager.openCalls++
	result := manager.result
	openErr := manager.openErr
	block := manager.block
	manager.mu.Unlock()
	if block != nil {
		<-block
	}
	if result.Port == 0 {
		result = firewall.Result{Backend: "test", Port: port, Created: true}
		manager.mu.Lock()
		manager.result = result
		manager.mu.Unlock()
	}
	return result, openErr
}

func (manager *completionFirewall) Rollback(_ context.Context, _ firewall.Result) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.rollbackCalls++
	return manager.rollbackErr
}

func (manager *completionFirewall) counts() (int, int) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.openCalls, manager.rollbackCalls
}

func (manager *completionFirewall) setBlock(block chan struct{}) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.block = block
}

type completionFixture struct {
	base          config.Config
	configPath    string
	urlPath       string
	store         *completionMemorySecrets
	database      *DatabaseVerificationManager
	dns           *setup.DNSVerificationManager
	databaseToken string
	dnsToken      string
	databaseRef   string
	dnsRef        string
	sessions      *SessionManager
	sessionCookie string
	issuer        *completionCertificateIssuer
	firewall      *completionFirewall
}

func newCompletionFixture(t *testing.T, domain string) completionFixture {
	t.Helper()
	baseDir := t.TempDir()
	base := config.Default(baseDir)
	configPath := filepath.Join(base.Paths.Config, "scert.json")
	store := newCompletionMemorySecrets()
	databaseManager := NewDatabaseVerificationManager(base, store)
	databaseManager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	databaseResult, err := databaseManager.Verify(context.Background(), DatabaseInput{Driver: "sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	dnsManager := setup.NewDNSVerificationManager(completionDNSTester{}, store)
	dnsResult, err := dnsManager.Verify(context.Background(), setup.DNSVerificationInput{
		ProviderID: "aliyun", TestDomain: domain,
		Credentials: map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions, oneTimeToken, err := NewSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Exchange(oneTimeToken, "192.0.2.1:5000")
	if err != nil {
		t.Fatal(err)
	}
	return completionFixture{
		base: base, configPath: configPath, urlPath: filepath.Join(base.Paths.Data, "bootstrap-url"),
		store: store, database: databaseManager, dns: dnsManager,
		databaseToken: databaseResult.VerificationToken, dnsToken: dnsResult.VerificationToken,
		databaseRef: databaseResult.CredentialRef, dnsRef: dnsResult.CredentialRef,
		sessions: sessions, sessionCookie: session.Token,
		issuer:   &completionCertificateIssuer{root: base.Paths.Certificate},
		firewall: &completionFirewall{},
	}
}

func (fixture completionFixture) completer(overrides func(*CompleteDependencies)) *Completer {
	dependencies := CompleteDependencies{
		BaseConfig: fixture.base, ConfigPath: fixture.configPath, BootstrapURLPath: fixture.urlPath,
		DatabaseSessions: fixture.database, DNSSessions: fixture.dns, Credentials: fixture.store,
		Sessions: fixture.sessions, Firewall: fixture.firewall, Certificate: fixture.issuer,
	}
	if overrides != nil {
		overrides(&dependencies)
	}
	return NewCompleter(dependencies)
}

func (fixture completionFixture) request(domain string) CompleteRequest {
	return CompleteRequest{
		DatabaseVerificationToken: fixture.databaseToken,
		DNSVerificationToken:      fixture.dnsToken,
		AdminUsername:             "owner", AdminEmail: "owner@example.test", AdminPassword: "Admin2026!",
		ServerPort: 8443, WebHostname: domain, WebEnabled: true,
		HTTPSMode: "direct", ACMEEnvironment: "staging",
		PropagationTimeoutSecs: 60, PropagationPollingIntervalSecs: 2,
	}
}

func TestCompleterCommitsFullInitializationAndRevokesBootstrapSession(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	if err := os.MkdirAll(filepath.Dir(fixture.urlPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.urlPath, []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.completer(nil).Complete(context.Background(), fixture.request("admin.example.test"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || result.ManagementURL != "https://admin.example.test:8443/" || len(result.Warnings) != 0 {
		t.Fatalf("初始化结果不正确：%+v", result)
	}
	stored, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.SetupReady || !stored.Web.InitReady || stored.Database.Driver != "sqlite" ||
		stored.DNS.CredentialRef != fixture.dnsRef || stored.Server.TLSCert != fixture.issuer.saved.FullChainPath {
		t.Fatalf("正式配置未完整提交：%+v", stored)
	}
	if fixture.sessions.Authenticate(fixture.sessionCookie) {
		t.Fatal("初始化成功后旧 Bootstrap 会话仍然有效")
	}
	if !fixture.store.has(fixture.dnsRef) {
		t.Fatal("正式 DNS 凭据被批量清理误删")
	}
	if _, err := os.Stat(fixture.urlPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Bootstrap 地址文件未清理：%v", err)
	}
	_, rollbackCalls := fixture.firewall.counts()
	if rollbackCalls != 0 {
		t.Fatal("初始化成功后不应回滚防火墙")
	}
}

func TestCompleterRejectsDNSDomainMismatchBeforeDatabaseSideEffects(t *testing.T) {
	fixture := newCompletionFixture(t, "verified.example.test")
	openCalls := 0
	_, err := fixture.completer(func(dependencies *CompleteDependencies) {
		dependencies.OpenDatabase = func(context.Context, config.DatabaseConfig) (*database.Database, error) {
			openCalls++
			return nil, errors.New("must not run")
		}
	}).Complete(context.Background(), fixture.request("other.example.test"))
	firewallOpenCalls, _ := fixture.firewall.counts()
	if err == nil || openCalls != 0 || fixture.issuer.calls != 0 || firewallOpenCalls != 0 {
		t.Fatalf("域名不匹配未在副作用前拒绝：err=%v open=%d", err, openCalls)
	}
	if fixture.store.has(fixture.dnsRef) {
		t.Fatal("失败初始化的 DNS 临时凭据未回收")
	}
}

func TestCompleterRollsBackCertificateAndFirewallWhenConfigSaveFails(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	_, err := fixture.completer(func(dependencies *CompleteDependencies) {
		dependencies.SaveConfig = func(config.Config, string) error { return errors.New("save failed") }
	}).Complete(context.Background(), fixture.request("admin.example.test"))
	if err == nil {
		t.Fatal("配置保存失败未中止初始化")
	}
	_, rollbackCalls := fixture.firewall.counts()
	if rollbackCalls != 1 {
		t.Fatalf("新防火墙规则未回滚：%d", rollbackCalls)
	}
	for _, path := range []string{fixture.issuer.saved.CertificatePath, fixture.issuer.saved.FullChainPath, fixture.issuer.saved.PrivateKeyPath} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("失败初始化的证书未回滚：%s err=%v", path, statErr)
		}
	}
	if fixture.store.has(fixture.dnsRef) {
		t.Fatal("配置保存失败后 DNS 临时凭据未回收")
	}
	if _, statErr := os.Stat(fixture.configPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("配置保存失败后仍留下正式配置：%v", statErr)
	}
}

func TestCompleterAllowsOnlyOneConcurrentCompletion(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	block := make(chan struct{})
	fixture.firewall.setBlock(block)
	completer := fixture.completer(nil)
	firstDone := make(chan error, 1)
	go func() {
		_, err := completer.Complete(context.Background(), fixture.request("admin.example.test"))
		firstDone <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		openCalls, _ := fixture.firewall.counts()
		if openCalls != 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	_, secondErr := completer.Complete(context.Background(), fixture.request("admin.example.test"))
	if !errors.Is(secondErr, ErrCompleteBusy) {
		t.Fatalf("并发初始化未返回 Busy：%v", secondErr)
	}
	close(block)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if _, err := completer.Complete(context.Background(), fixture.request("admin.example.test")); !errors.Is(err, ErrCompleteDone) {
		t.Fatalf("初始化完成后未返回 Done：%v", err)
	}
}

func TestCompleterPostCommitCleanupFailureDoesNotRollbackFormalConfig(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	result, err := fixture.completer(func(dependencies *CompleteDependencies) {
		dependencies.RemoveFile = func(string) error { return errors.New("remove failed") }
	}).Complete(context.Background(), fixture.request("admin.example.test"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "Bootstrap") {
		t.Fatalf("提交后清理失败没有返回安全警告：%+v", result)
	}
	stored, err := config.Load(fixture.configPath)
	_, rollbackCalls := fixture.firewall.counts()
	if err != nil || !stored.SetupReady || rollbackCalls != 0 {
		t.Fatalf("提交后清理失败错误回滚了正式配置：cfg=%+v err=%v", stored, err)
	}
}

func TestCompleterSupportsPublicIPManagementCertificate(t *testing.T) {
	fixture := newCompletionFixture(t, "certificate-zone.example.test")
	request := fixture.request("")
	request.WebPublicIP = "203.0.113.70"
	result, err := fixture.completer(nil).Complete(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ManagementURL != "https://203.0.113.70:8443/" {
		t.Fatalf("公网 IP 管理地址不正确：%s", result.ManagementURL)
	}
	stored, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Web.PublicIP != "203.0.113.70" || stored.Web.Hostname != "" || !strings.Contains(stored.Server.TLSCert, "management-ip") {
		t.Fatalf("公网 IP 初始化配置不正确：%+v", stored)
	}
}

func completionDomainResource(name string) certificate.Resource {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		panic(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		panic(err)
	}
	return certificate.Resource{
		Domains: []string{name}, Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}),
		NotBefore:  template.NotBefore, NotAfter: template.NotAfter,
	}
}
