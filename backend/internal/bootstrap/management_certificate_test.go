package bootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGenerateManagementCertificateUsesOnlyRequestedIPSAN(t *testing.T) {
	root := filepath.Join(t.TempDir(), "certificates")
	saved, err := GenerateManagementCertificate(context.Background(), root, "203.0.113.25")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(saved.CertificatePath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("管理证书不是 PEM 格式")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.DNSNames) != 0 || len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("203.0.113.25")) {
		t.Fatalf("管理证书 SAN 超出目标 IP：DNS=%v IP=%v", leaf.DNSNames, leaf.IPAddresses)
	}
	publicKey, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		t.Fatalf("管理证书未使用 ECDSA P-256：%T", leaf.PublicKey)
	}
	if duration := leaf.NotAfter.Sub(leaf.NotBefore); duration < managementCertificateLifetime || duration > managementCertificateLifetime+10*time.Minute {
		t.Fatalf("管理证书有效期不符合预期：%s", duration)
	}
	if saved.TargetIP != "203.0.113.25" || saved.CertificatePath == saved.PrivateKeyPath {
		t.Fatalf("管理证书结果不完整：%+v", saved)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(saved.PrivateKeyPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("私钥权限不是 0600：%04o", info.Mode().Perm())
		}
	}
}

func TestGenerateManagementCertificateRejectsNonPublicAddresses(t *testing.T) {
	for _, value := range []string{"", "not-an-ip", "127.0.0.1", "::1", "10.0.0.8", "192.168.1.9", "169.254.1.2", "224.0.0.1"} {
		t.Run(value, func(t *testing.T) {
			_, err := GenerateManagementCertificate(context.Background(), filepath.Join(t.TempDir(), "certificates"), value)
			if err == nil {
				t.Fatalf("非公网 IP 未被拒绝：%q", value)
			}
		})
	}
}

func TestGenerateManagementCertificateRejectsSymlinkTarget(t *testing.T) {
	root := filepath.Join(t.TempDir(), "certificates")
	targetDirectory := filepath.Join(root, "management-ip", "203.0.113.31")
	if err := os.MkdirAll(targetDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.pem")
	if err := os.WriteFile(external, []byte("do-not-touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(targetDirectory, "key.pem")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("当前 Windows 环境无法创建符号链接：%v", err)
		}
		t.Fatal(err)
	}
	if _, err := GenerateManagementCertificate(context.Background(), root, "203.0.113.31"); err == nil {
		t.Fatal("符号链接私钥目标未被拒绝")
	}
	data, err := os.ReadFile(external)
	if err != nil || string(data) != "do-not-touch" {
		t.Fatalf("符号链接外部文件被修改：%q err=%v", data, err)
	}
}

func TestRollbackManagementCertificateRestoresExactPreviousFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "certificates")
	directory := filepath.Join(root, "management-ip", "203.0.113.42")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	previous := map[string]string{
		"cert.pem":      "previous-certificate",
		"fullchain.pem": "previous-fullchain",
		"key.pem":       "previous-private-key",
	}
	for name, value := range previous {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := GenerateManagementCertificate(context.Background(), root, "203.0.113.42")
	if err != nil {
		t.Fatal(err)
	}
	if err := RollbackManagementCertificate(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	for name, value := range previous {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(data) != value {
			t.Fatalf("回滚未恢复 %s：%q err=%v", name, data, err)
		}
	}
}

func TestRollbackManagementCertificateRemovesNewFilesOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "certificates")
	saved, err := GenerateManagementCertificate(context.Background(), root, "203.0.113.50")
	if err != nil {
		t.Fatal(err)
	}
	if err := RollbackManagementCertificate(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	for _, path := range managementCertificatePaths(saved) {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("新管理证书文件未被回滚：%s err=%v", path, err)
		}
	}
}
