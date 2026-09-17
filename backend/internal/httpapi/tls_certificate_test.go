package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type rotationFixture struct {
	server           *Server
	address          string
	root             *x509.Certificate
	key              *ecdsa.PrivateKey
	pool             *x509.CertPool
	directory        string
	firstCertificate string
	firstKey         string
}

func newRotationFixture(t *testing.T) *rotationFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Scert test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &rotationFixture{root: root, key: key, directory: t.TempDir(), pool: x509.NewCertPool()}
	fixture.pool.AddCert(root)
	fixture.firstCertificate, fixture.firstKey = fixture.issue(t, 2, "management.example.test", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	cfg := readyTestConfig(fixture.directory)
	cfg.Admin = config.AdminConfig{Username: "admin", Email: "admin@example.test", PasswordHash: "test-only-hash"}
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "management.example.test"
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.TLSCert, cfg.Server.TLSKey = fixture.firstCertificate, fixture.firstKey
	configPath := filepath.Join(fixture.directory, "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}
	fixture.server = New(Dependencies{Config: cfg, ConfigPath: configPath})
	fixture.server.tlsRoots = fixture.pool
	server, err := fixture.server.prepareHTTPServer()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture.address = listener.Addr().String()
	done := make(chan error, 1)
	go func() { done <- server.ServeTLS(listener, "", "") }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := fixture.server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("HTTPS 服务退出异常：%v", err)
		}
	})
	return fixture
}

func (f *rotationFixture) issue(t *testing.T, serial int64, host string, before, after time.Time) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(serial), DNSNames: []string{host},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   before, NotAfter: after,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, f.root, &key.PublicKey, f.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePath := filepath.Join(f.directory, fmt.Sprintf("%d.pem", serial))
	keyPath := filepath.Join(f.directory, fmt.Sprintf("%d.key", serial))
	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.root.Raw})...)
	if err := os.WriteFile(certificatePath, chain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificatePath, keyPath
}

func (f *rotationFixture) serial() (int64, error) {
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, &tls.Config{
		RootCAs: f.pool, ServerName: "management.example.test", MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return 0, err
	}
	defer connection.Close()
	return connection.ConnectionState().PeerCertificates[0].SerialNumber.Int64(), nil
}

func TestTLSCertificateRotationPersistsAndServesWithoutRestart(t *testing.T) {
	f := newRotationFixture(t)
	if serial, err := f.serial(); err != nil || serial != 2 {
		t.Fatalf("初始证书异常：%d %v", serial, err)
	}
	certificatePath, keyPath := f.issue(t, 3, "management.example.test", time.Now().Add(-time.Hour), time.Now().Add(30*time.Hour))
	before := f.server.httpServer
	if err := f.server.ActivateTLSCertificate(certificatePath, keyPath); err != nil {
		t.Fatal(err)
	}
	if serial, err := f.serial(); err != nil || serial != 3 {
		t.Fatalf("新握手未使用新证书：%d %v", serial, err)
	}
	if f.server.httpServer != before {
		t.Fatal("证书更新不应替换 HTTP 服务")
	}
	stored, err := config.Load(f.server.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Server.TLSCert != certificatePath || stored.Server.TLSKey != keyPath {
		t.Fatal("重启所需证书路径未持久化")
	}
	state, err := f.server.ManagementCertificate()
	if err != nil || state.CertificatePath != certificatePath || state.NotAfter.IsZero() {
		t.Fatalf("续期读取的证书状态异常：%+v %v", state, err)
	}
	if err := f.server.ActivateTLSCertificate(f.firstCertificate, f.firstKey); err != nil {
		t.Fatal(err)
	}
	if serial, err := f.serial(); err != nil || serial != 2 {
		t.Fatalf("旧证书回退失败：%d %v", serial, err)
	}
	stored, err = config.Load(f.server.configPath)
	if err != nil || stored.Server.TLSCert != f.firstCertificate {
		t.Fatalf("回退未持久化：%v", err)
	}
}

func TestTLSCertificateRotationRejectsInvalidCandidates(t *testing.T) {
	f := newRotationFixture(t)
	now := time.Now()
	tests := []struct {
		name, host    string
		before, after time.Time
	}{
		{"wrong-host", "other.example.test", now.Add(-time.Hour), now.Add(time.Hour)},
		{"expired", "management.example.test", now.Add(-2 * time.Hour), now.Add(-time.Hour)},
		{"future", "management.example.test", now.Add(time.Hour), now.Add(2 * time.Hour)},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			certPath, keyPath := f.issue(t, int64(i+10), test.host, test.before, test.after)
			if err := f.server.ActivateTLSCertificate(certPath, keyPath); err == nil {
				t.Fatal("无效证书不应生效")
			}
		})
	}
	_, wrongKey := f.issue(t, 20, "management.example.test", now.Add(-time.Hour), now.Add(time.Hour))
	if err := f.server.ActivateTLSCertificate(f.firstCertificate, wrongKey); err == nil {
		t.Fatal("不匹配的私钥不应生效")
	}
	if err := f.server.ActivateTLSCertificate(filepath.Join(f.directory, "missing.pem"), wrongKey); err == nil {
		t.Fatal("缺失的证书不应生效")
	}
	if serial, err := f.serial(); err != nil || serial != 2 {
		t.Fatalf("失败后旧证书不可用：%d %v", serial, err)
	}
	stored, err := config.Load(f.server.configPath)
	if err != nil || stored.Server.TLSCert != f.firstCertificate {
		t.Fatalf("失败不应修改配置：%v", err)
	}
}

func TestTLSCertificateRotationPreservesStateWhenPersistenceFails(t *testing.T) {
	f := newRotationFixture(t)
	certificatePath, keyPath := f.issue(t, 3, "management.example.test", time.Now().Add(-time.Hour), time.Now().Add(30*time.Hour))
	// 用目录占据目标文件，跨平台触发确定的持久化失败。
	f.server.configPath = t.TempDir()
	if err := f.server.ActivateTLSCertificate(certificatePath, keyPath); err == nil {
		t.Fatal("配置写入失败不应报告成功")
	}
	if serial, err := f.serial(); err != nil || serial != 2 {
		t.Fatalf("配置写入失败后旧证书不可用：%d %v", serial, err)
	}
	state, err := f.server.ManagementCertificate()
	if err != nil || state.CertificatePath != f.firstCertificate {
		t.Fatalf("配置写入失败不应修改内存：%+v %v", state, err)
	}
}

func TestTLSCertificateRotationConcurrentHandshakes(t *testing.T) {
	f := newRotationFixture(t)
	certificatePath, keyPath := f.issue(t, 3, "management.example.test", time.Now().Add(-time.Hour), time.Now().Add(30*time.Hour))
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 15 {
				serial, err := f.serial()
				if err != nil || (serial != 2 && serial != 3) {
					t.Errorf("并发握手失败：%d %v", serial, err)
					return
				}
			}
		}()
	}
	for range 6 {
		if err := f.server.ActivateTLSCertificate(certificatePath, keyPath); err != nil {
			t.Error(err)
		}
		if err := f.server.ActivateTLSCertificate(f.firstCertificate, f.firstKey); err != nil {
			t.Error(err)
		}
	}
	workers.Wait()
}

func TestTLSCertificateRotationRejectsUntrustedChain(t *testing.T) {
	f := newRotationFixture(t)
	certificatePath, keyPath := f.issue(t, 3, "management.example.test", time.Now().Add(-time.Hour), time.Now().Add(30*time.Hour))
	f.server.tlsRoots = x509.NewCertPool()
	if err := f.server.ActivateTLSCertificate(certificatePath, keyPath); err == nil {
		t.Fatal("不可信证书链不应生效")
	}
	if serial, err := f.serial(); err != nil || serial != 2 {
		t.Fatalf("不可信更新不应影响旧证书：%d %v", serial, err)
	}
}

func TestTLSCertificateRotationWithoutSNI(t *testing.T) {
	f := newRotationFixture(t)
	certificatePath, keyPath := f.issue(t, 3, "management.example.test", time.Now().Add(-time.Hour), time.Now().Add(30*time.Hour))
	if err := f.server.ActivateTLSCertificate(certificatePath, keyPath); err != nil {
		t.Fatal(err)
	}
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, &tls.Config{
		RootCAs: f.pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if serial := connection.ConnectionState().PeerCertificates[0].SerialNumber.Int64(); serial != 3 {
		t.Fatalf("无 SNI 的握手未使用新证书：%d", serial)
	}
}

func TestTLSCertificateRotationSurvivesServerRestart(t *testing.T) {
	f := newRotationFixture(t)
	certificatePath, keyPath := f.issue(t, 3, "management.example.test", time.Now().Add(-time.Hour), time.Now().Add(30*time.Hour))
	if err := f.server.ActivateTLSCertificate(certificatePath, keyPath); err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load(f.server.configPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := f.server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := New(Dependencies{Config: stored, ConfigPath: f.server.configPath})
	server, err := restarted.prepareHTTPServer()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.ServeTLS(listener, "", "") }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := restarted.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	})
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", listener.Addr().String(), &tls.Config{
		RootCAs: f.pool, ServerName: "management.example.test", MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if serial := connection.ConnectionState().PeerCertificates[0].SerialNumber.Int64(); serial != 3 {
		t.Fatalf("重新启动未使用已持久化的新证书：%d", serial)
	}
}
