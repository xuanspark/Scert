package app

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
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/nginxactivation"
)

type applicationTLSFixture struct {
	root      string
	config    config.Config
	path      string
	caPath    string
	oldLeaf   []byte
	newLeaf   []byte
	candidate certificate.SavedResource
	upstream  nginxactivation.Endpoint
	proxy     nginxactivation.Endpoint
}

func writeApplicationTLSFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func applicationTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func newApplicationTLSFixture(t *testing.T) *applicationTLSFixture {
	t.Helper()
	cfg := readyApplicationConfig(t)
	for _, path := range []string{cfg.Paths.Config, cfg.Paths.Certificate, filepath.Join(cfg.Paths.Install, "empty-roots")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	authority := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Scert integration CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(7 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, authority, authority, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	authority, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	writeApplicationTLSFile(t, filepath.Join(cfg.Paths.Install, "ca.pem"), caPEM)
	for index, name := range []string{"previous", "candidate"} {
		version := filepath.Join(cfg.Paths.Certificate, name)
		if err := os.Mkdir(version, 0o700); err != nil {
			t.Fatal(err)
		}
		leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		leaf := &x509.Certificate{
			SerialNumber: big.NewInt(int64(index + 2)), DNSNames: []string{cfg.Web.Hostname},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Duration(index+1) * 24 * time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		leafDER, err := x509.CreateCertificate(rand.Reader, leaf, authority, &leafKey.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
		if err != nil {
			t.Fatal(err)
		}
		chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), caPEM...)
		writeApplicationTLSFile(t, filepath.Join(version, "fullchain.pem"), chain)
		writeApplicationTLSFile(t, filepath.Join(version, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	}
	cfg.Server.Host, cfg.Server.HTTPSMode = "127.0.0.1", "proxy"
	cfg.Server.Port, cfg.Server.ProxyHTTPSPort = applicationTestPort(t), applicationTestPort(t)
	for cfg.Server.ProxyHTTPSPort == cfg.Server.Port {
		cfg.Server.ProxyHTTPSPort = applicationTestPort(t)
	}
	cfg.Server.TLSCert = filepath.Join(cfg.Paths.Certificate, "previous", "fullchain.pem")
	cfg.Server.TLSKey = filepath.Join(cfg.Paths.Certificate, "previous", "key.pem")
	if err := cfg.Save(filepath.Join(cfg.Paths.Config, "scert.json")); err != nil {
		t.Fatal(err)
	}
	return loadApplicationTLSFixture(t, cfg.Paths.Install)
}

func loadApplicationTLSFixture(t *testing.T, root string) *applicationTLSFixture {
	t.Helper()
	path := filepath.Join(root, "conf", "scert.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &applicationTLSFixture{
		root: root, config: cfg, path: path, caPath: filepath.Join(root, "ca.pem"),
		candidate: certificate.SavedResource{
			FullChainPath:  filepath.Join(cfg.Paths.Certificate, "candidate", "fullchain.pem"),
			PrivateKeyPath: filepath.Join(cfg.Paths.Certificate, "candidate", "key.pem"),
		},
	}
	caPEM, err := os.ReadFile(f.caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("测试 CA 无法解析")
	}
	previous, err := tls.LoadX509KeyPair(cfg.Server.TLSCert, cfg.Server.TLSKey)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := tls.LoadX509KeyPair(f.candidate.FullChainPath, f.candidate.PrivateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	f.oldLeaf, f.newLeaf = previous.Certificate[0], candidate.Certificate[0]
	f.upstream = nginxactivation.Endpoint{Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Server.Port)), Hostname: cfg.Web.Hostname, Roots: roots}
	f.proxy = nginxactivation.Endpoint{Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Server.ProxyHTTPSPort)), Hostname: cfg.Web.Hostname, Roots: roots}
	return f
}

func (f *applicationTLSFixture) start(t *testing.T) (*App, func()) {
	t.Helper()
	cfg, err := config.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewWithConfigPath(t.Context(), cfg, f.path)
	if err != nil {
		t.Fatal(err)
	}
	// 隔离测试不连接固定生产 socket；恢复场景随后接入本轮独立 IPC。
	if application.managementStop != nil {
		application.managementStop()
		<-application.managementDone
	}
	done := make(chan error, 1)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			if err := application.Close(); err != nil {
				t.Error(err)
			}
			select {
			case err := <-done:
				if !errors.Is(err, http.ErrServerClosed) {
					t.Errorf("应用 HTTPS 退出异常：%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("应用 HTTPS 未按时退出")
			}
		})
	}
	t.Cleanup(stop)
	go func() { done <- application.HTTP.ListenAndServe() }()
	select {
	case <-application.HTTP.Listening():
	case err := <-done:
		done <- err
		t.Fatalf("应用 HTTPS 启动失败：%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("应用 HTTPS 未按时启动")
	}
	return application, stop
}

func (f *applicationTLSFixture) seed(t *testing.T, application *App) {
	t.Helper()
	if err := application.SecretStore.Put(t.Context(), f.config.DNS.CredentialRef, map[string]string{
		"access_key_id": "integration-test-id", "access_key_secret": "integration-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.Database.DB.ExecContext(t.Context(), "CREATE TABLE activation_test (value TEXT NOT NULL); INSERT INTO activation_test VALUES ('preserved')"); err != nil {
		t.Fatal(err)
	}
}

func (f *applicationTLSFixture) assertDatabase(t *testing.T, application *App) {
	t.Helper()
	var value string
	if err := application.Database.DB.QueryRowContext(t.Context(), "SELECT value FROM activation_test").Scan(&value); err != nil || value != "preserved" {
		t.Fatal("应用重建后数据库内容未保留", err)
	}
}

func TestApplicationManagementListenerRestartsWithPersistentReadiness(t *testing.T) {
	f := newApplicationTLSFixture(t)
	first, stop := f.start(t)
	f.seed(t, first)
	if _, err := f.upstream.Check(t.Context(), f.oldLeaf); err != nil {
		t.Fatal(err)
	}
	stop()
	second, _ := f.start(t)
	if _, err := f.upstream.Check(t.Context(), f.oldLeaf); err != nil {
		t.Fatal("重建应用后 HTTPS、凭据与数据库就绪检查失败", err)
	}
	f.assertDatabase(t, second)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := second.Database.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}
