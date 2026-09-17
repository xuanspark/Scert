package nginxactivation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testHostname = "management.example.test"

type testAuthority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	roots       *x509.CertPool
}

type testMaterial struct {
	chain []byte
	key   []byte
	pair  tls.Certificate
}

func newAuthority(t *testing.T) testAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Scert test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return testAuthority{certificate: certificate, key: key, roots: roots}
}

func (a testAuthority) issue(t *testing.T, hostname string, before, after time.Time) testMaterial {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial, DNSNames: []string{hostname}, NotBefore: before, NotAfter: after,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.certificate, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	material := testMaterial{
		chain: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key:   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}
	material.chain = append(material.chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.certificate.Raw})...)
	material.pair, err = tls.X509KeyPair(material.chain, material.key)
	if err != nil {
		t.Fatal(err)
	}
	return material
}

type testEndpoint struct {
	Endpoint
	active atomic.Pointer[tls.Certificate]
	server *httptest.Server
}

func newTestEndpoint(t *testing.T, a testAuthority, material testMaterial, handler http.Handler) *testEndpoint {
	t.Helper()
	if handler == nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/readyz" || r.Method != http.MethodGet || r.TLS.ServerName != testHostname || !strings.HasPrefix(r.Host, testHostname+":") {
				http.Error(w, "请求无效", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"service":"scert","status":"ready"}`)
		})
	}
	e := &testEndpoint{}
	e.active.Store(&material.pair)
	e.server = httptest.NewUnstartedServer(handler)
	e.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	e.server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return e.active.Load(), nil
	}}
	e.server.StartTLS()
	t.Cleanup(e.server.Close)
	e.Endpoint = Endpoint{Address: e.server.Listener.Addr().String(), Hostname: testHostname, Roots: a.roots}
	return e
}

func TestEndpointStrictTLSAndReadiness(t *testing.T) {
	a := newAuthority(t)
	now := time.Now()
	material := a.issue(t, testHostname, now.Add(-time.Hour), now.Add(24*time.Hour))
	other := a.issue(t, testHostname, now.Add(-time.Hour), now.Add(48*time.Hour))
	for _, test := range []struct {
		name     string
		body     string
		status   int
		accepted bool
	}{
		{"ready", `{"service":"scert","status":"ready"}`, 200, true},
		{"wrong-service", `{"service":"other","status":"ready"}`, 200, false},
		{"not-ready", `{"service":"scert","status":"starting"}`, 200, false},
		{"redirect", `{"service":"scert","status":"ready"}`, 308, false},
		{"unavailable", `{"service":"scert","status":"ready"}`, 503, false},
		{"trailing-data", `{"service":"scert","status":"ready"}{}`, 200, false},
		{"large-body", strings.Repeat(" ", 4097), 200, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := newTestEndpoint(t, a, material, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			leaf, err := e.Check(context.Background(), material.pair.Certificate[0])
			if (err == nil) != test.accepted {
				t.Fatalf("accepted=%v, error=%v", test.accepted, err)
			}
			if test.accepted && !bytes.Equal(leaf, material.pair.Certificate[0]) {
				t.Fatal("叶证书不匹配")
			}
		})
	}
	e := newTestEndpoint(t, a, material, nil)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	if _, err := e.Check(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Check(context.Background(), other.pair.Certificate[0]); err == nil {
		t.Fatal("不应接受其他叶证书")
	}
	wrongHost := e.Endpoint
	wrongHost.Hostname = "other.example.test"
	if _, err := wrongHost.Check(context.Background(), nil); err == nil {
		t.Fatal("不应接受错误主机")
	}
	wrongRoot := e.Endpoint
	wrongRoot.Roots = x509.NewCertPool()
	if _, err := wrongRoot.Check(context.Background(), nil); err == nil {
		t.Fatal("不应接受不可信证书")
	}
}

func TestEndpointWaitAndCancellation(t *testing.T) {
	a := newAuthority(t)
	now := time.Now()
	old := a.issue(t, testHostname, now.Add(-time.Hour), now.Add(24*time.Hour))
	newMaterial := a.issue(t, testHostname, now.Add(-time.Hour), now.Add(48*time.Hour))
	e := newTestEndpoint(t, a, old, nil)
	switched := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		e.active.Store(&newMaterial.pair)
		close(switched)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.Wait(ctx, newMaterial.pair.Certificate[0]); err != nil {
		t.Fatal(err)
	}
	<-switched
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if err := e.Wait(cancelled, old.pair.Certificate[0]); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未传播：%v", err)
	}
	if err := e.Wait(ctx, nil); err == nil {
		t.Fatal("缺少预期证书应被拒绝")
	}
}

func TestEndpointRejectsUnsafePolicies(t *testing.T) {
	for _, address := range []string{"example.test:443", "192.0.2.1:443", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:+443", "127.0.0.1:0443", "[::ffff:127.0.0.1]:443", "127.0.0.1"} {
		if err := (Endpoint{Address: address, Hostname: testHostname}).validate(); err == nil {
			t.Fatalf("不应接受地址 %q", address)
		}
	}
	for _, hostname := range []string{"*.example.test", "127.0.0.1", " management.example.test", "MANAGEMENT.example.test", "example.test/path"} {
		if err := (Endpoint{Address: "127.0.0.1:443", Hostname: hostname}).validate(); err == nil {
			t.Fatalf("不应接受域名 %q", hostname)
		}
	}
	for _, address := range []string{"127.0.0.1:443", "[::1]:443"} {
		if err := (Endpoint{Address: address, Hostname: testHostname}).validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEndpointWaitRequiresConsecutiveMatches(t *testing.T) {
	a := newAuthority(t)
	now := time.Now()
	old := a.issue(t, testHostname, now.Add(-time.Hour), now.Add(24*time.Hour))
	candidate := a.issue(t, testHostname, now.Add(-time.Hour), now.Add(48*time.Hour))
	var requests atomic.Int32
	var endpoint *testEndpoint
	endpoint = newTestEndpoint(t, a, candidate, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			endpoint.active.Store(&old.pair)
		} else {
			endpoint.active.Store(&candidate.pair)
		}
		_, _ = io.WriteString(w, `{"service":"scert","status":"ready"}`)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := endpoint.Wait(ctx, candidate.pair.Certificate[0]); err != nil {
		t.Fatal(err)
	}
	if requests.Load() < 5 {
		t.Fatal("出现旧证书后未重新累计连续成功探测", requests.Load())
	}
}
