package certificate

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestNormalizeDomainsSupportsSANAndWildcard(t *testing.T) {
	got, err := NormalizeDomains([]string{"Example.COM.", "*.example.com", "example.com"})
	if err != nil {
		t.Fatalf("规范化域名失败：%v", err)
	}
	if len(got) != 2 || got[0] != "*.example.com" || got[1] != "example.com" {
		t.Fatalf("规范化结果错误：%v", got)
	}
}

func TestFileStoreSaveBacksUpAndKeepsKeyRestricted(t *testing.T) {
	root := t.TempDir()
	store := FileStore{Root: filepath.Join(root, "ssl"), BackupRoot: filepath.Join(root, "backups"), Now: func() time.Time { return time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC) }}
	resource := testResource(t, "example.com")
	first, err := store.Save(context.Background(), "example.com", resource)
	if err != nil {
		t.Fatalf("第一次保存失败：%v", err)
	}
	if first.BackupDirectory != "" {
		t.Fatal("第一次保存不应生成备份")
	}
	resource.Certificate = append([]byte(nil), resource.Certificate...)
	second, err := store.Save(context.Background(), "example.com", resource)
	if err != nil {
		t.Fatalf("第二次保存失败：%v", err)
	}
	if second.BackupDirectory == "" {
		t.Fatal("替换已有证书时应生成备份")
	}
	if _, err := os.Stat(second.FullChainPath); err != nil {
		t.Fatalf("完整证书链不存在：%v", err)
	}
	if info, err := os.Stat(second.PrivateKeyPath); err != nil {
		t.Fatalf("私钥不存在：%v", err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("私钥权限错误：%o", info.Mode().Perm())
	}
}

func TestValidateResourceRejectsMismatchedKey(t *testing.T) {
	resource := testResource(t, "example.com")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	resource.PrivateKey = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if _, err := ValidateResource(resource); err == nil {
		t.Fatal("证书与私钥不匹配时应失败")
	}
}

func TestValidateResourceForDomainsSupportsWildcard(t *testing.T) {
	resource := testResourceForWildcard(t, "*.example.com")
	if _, err := ValidateResourceForDomains(resource, []string{"*.example.com"}); err != nil {
		t.Fatalf("泛域名证书覆盖校验失败：%v", err)
	}
}

func testResource(t *testing.T, name string) Resource {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return Resource{Domains: []string{name}, Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})}
}

func testResourceForWildcard(t *testing.T, name string) Resource {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return Resource{Domains: []string{name}, Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})}
}
