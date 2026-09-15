package certificate

import (
	"crypto/x509"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func testResourceRoots(t *testing.T, resource Resource) *x509.CertPool {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(resource.Certificate) {
		t.Fatal("测试证书无法加入专用测试信任库")
	}
	return roots
}

func TestACMETrustRootsAreEnvironmentIsolated(t *testing.T) {
	staging, err := acmeTrustRoots("staging")
	if err != nil || staging == nil {
		t.Fatalf("测试环境根加载失败：%v", err)
	}
	production, err := acmeTrustRoots("production")
	if err != nil || production != nil {
		t.Fatal("正式环境必须使用系统信任库，不能继承测试根")
	}
	entries, err := stagingTrust.ReadDir("trust")
	if err != nil || len(entries) != 4 {
		t.Fatalf("必须包含四个官方测试根：%v", err)
	}
	for _, entry := range entries {
		data, err := stagingTrust.ReadFile("trust/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		certs, err := parseCertificates(data)
		if err != nil || len(certs) != 1 {
			t.Fatalf("测试根文件无效：%s", entry.Name())
		}
		root := certs[0]
		if !root.IsCA || !strings.Contains(root.Subject.CommonName, "(STAGING)") || root.CheckSignatureFrom(root) != nil {
			t.Fatalf("测试根身份或自签名无效：%s", entry.Name())
		}
		if err := verifyChain(certs, staging, time.Now()); err != nil {
			t.Fatalf("官方测试根应受测试信任库信任：%v", err)
		}
		if err := verifyChain(certs, production, time.Now()); err == nil {
			t.Fatal("官方测试根不能受正式信任库信任")
		}
	}
	if _, err := acmeTrustRoots("unknown"); err == nil {
		t.Fatal("未知环境必须失败关闭")
	}
}

func TestACMERejectsUntrustedLeafEvenWithoutSeparateIssuer(t *testing.T) {
	service := NewService(config.Default(t.TempDir()), nil, nil, nil, nil)
	resource := testResource(t, "example.com")
	for _, environment := range []string{"production", "staging"} {
		if err := service.validateACMEResource(t.Context(), resource, []string{"example.com"}, environment); err == nil {
			t.Fatalf("%s 不得因 issuer 字段为空而接受任意自签证书", environment)
		}
	}
	service.acmeRoots = testResourceRoots(t, resource)
	if err := service.validateACMEResource(t.Context(), resource, []string{"example.com"}, "staging"); err != nil {
		t.Fatalf("专用测试根验证失败：%v", err)
	}
}
