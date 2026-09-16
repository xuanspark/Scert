package bootstrap

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type testAddress string

func (a testAddress) Network() string { return "test" }
func (a testAddress) String() string  { return string(a) }

func TestTemporaryTLSMaterialUsesECDSAP256AndExpectedIPSANs(t *testing.T) {
	previous := interfaceAddresses
	interfaceAddresses = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("10.20.30.40"), Mask: net.CIDRMask(24, 32)},
			testAddress("2001:db8::20/64"),
			testAddress("0.0.0.0/0"),
		}, nil
	}
	t.Cleanup(func() { interfaceAddresses = previous })

	material, err := NewTemporaryTLSMaterial("203.0.113.18")
	if err != nil {
		t.Fatalf("生成 Bootstrap 临时证书失败：%v", err)
	}
	privateKey, ok := material.Certificate.PrivateKey.(*ecdsa.PrivateKey)
	if !ok || privateKey.Curve != elliptic.P256() {
		t.Fatalf("Bootstrap 临时证书未使用 ECDSA P-256：%T", material.Certificate.PrivateKey)
	}
	if material.Leaf == nil || material.Leaf.CheckSignature(material.Leaf.SignatureAlgorithm, material.Leaf.RawTBSCertificate, material.Leaf.Signature) != nil {
		t.Fatal("Bootstrap 临时证书不是有效的自签名证书")
	}
	for _, expected := range []string{"127.0.0.1", "::1", "10.20.30.40", "2001:db8::20", "203.0.113.18"} {
		if !containsIP(material.Leaf.IPAddresses, expected) {
			t.Fatalf("Bootstrap 临时证书缺少 IP SAN：%s，实际：%v", expected, material.Leaf.IPAddresses)
		}
	}
	if containsIP(material.Leaf.IPAddresses, "0.0.0.0") {
		t.Fatal("Bootstrap 临时证书不应包含未指定地址")
	}
}

func TestBootstrapURLKeepsTokenInFragment(t *testing.T) {
	value, err := bootstrapURL(net.ParseIP("2001:db8::8"), 8080, "one-time-token")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "[2001:db8::8]:8080" || parsed.RawQuery != "" {
		t.Fatalf("Bootstrap 地址格式不正确：%s", value)
	}
	if parsed.Fragment != "token=one-time-token" {
		t.Fatalf("一次性令牌未放在 URL Fragment：%s", value)
	}
}

func TestWriteBootstrapURLRejectsSymlinkAndUsesPrivateMode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "run", "bootstrap-url")
	if err := WriteBootstrapURL(path, "https://127.0.0.1:8080/#token=test"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "https://127.0.0.1:8080/#token=test" {
		t.Fatalf("Bootstrap 地址文件内容错误：%q", data)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("Bootstrap 地址文件权限错误：%o", info.Mode().Perm())
		}
	}

	symlinkPath := filepath.Join(directory, "bootstrap-link")
	if err := os.Symlink(path, symlinkPath); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("当前 Windows 环境不允许创建符号链接")
		}
		t.Fatal(err)
	}
	if err := WriteBootstrapURL(symlinkPath, "https://127.0.0.1:8080/#token=other"); err == nil {
		t.Fatal("Bootstrap 地址文件符号链接未被拒绝")
	}
}

func containsIP(values []net.IP, expected string) bool {
	wanted := net.ParseIP(expected)
	for _, value := range values {
		if value.Equal(wanted) {
			return true
		}
	}
	return false
}
