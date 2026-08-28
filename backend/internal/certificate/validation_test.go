package certificate

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLocalValidatorChecksKeyChainAndSAN(t *testing.T) {
	certificateData, privateKeyData, leaf := validationTestCertificate(t, []string{"example.com", "www.example.com"})
	rootPool := x509.NewCertPool()
	rootPool.AddCert(leaf)
	validator := LocalValidator{Roots: rootPool, VerifyChain: true}

	result, err := validator.ValidatePEM(context.Background(), certificateData, []string{"example.com", "www.example.com"})
	if err != nil {
		t.Fatalf("valid certificate should pass: %v", err)
	}
	if result.SerialNumber == "" || result.ChainLength != 1 || len(result.Domains) != 2 {
		t.Fatalf("unexpected validation result: %+v", result)
	}
	if _, err := tls.X509KeyPair(certificateData, privateKeyData); err != nil {
		t.Fatalf("test certificate and key should match: %v", err)
	}
}

func TestLocalValidatorRejectsMissingSAN(t *testing.T) {
	certificateData, _, leaf := validationTestCertificate(t, []string{"example.com"})
	rootPool := x509.NewCertPool()
	rootPool.AddCert(leaf)
	_, err := (LocalValidator{Roots: rootPool, VerifyChain: true}).ValidatePEM(context.Background(), certificateData, []string{"api.example.com"})
	if err == nil {
		t.Fatal("certificate without requested SAN should fail")
	}
}

func TestRemoteValidatorChecksPresentedCertificate(t *testing.T) {
	certificateData, privateKeyData, leaf := validationTestCertificate(t, []string{"localhost", "127.0.0.1"})
	pair, err := tls.X509KeyPair(certificateData, privateKeyData)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	server.StartTLS()
	defer server.Close()

	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	result, err := (RemoteValidator{Roots: roots, Timeout: 5 * time.Second}).ValidateHTTPS(context.Background(), server.URL, []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("remote certificate should pass: %v", err)
	}
	if result.ChainLength != 1 || len(result.FingerprintSHA256) != 64 {
		t.Fatalf("unexpected remote validation result: %+v", result)
	}
}

func validationTestCertificate(t *testing.T, names []string) ([]byte, []byte, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(99),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	certificateData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKeyData := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certificateData, privateKeyData, leaf
}
