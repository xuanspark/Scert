package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultPaths(t *testing.T) {
	paths := DefaultPaths(t.TempDir())
	if runtime.GOOS == "linux" {
		if paths.Install != "/scert" || paths.Certificate != "/ssl" {
			t.Fatalf("Linux 默认路径不正确：%+v", paths)
		}
		return
	}
	if filepath.Base(paths.Install) != "Scert" || filepath.Base(paths.Certificate) != "ssl" {
		t.Fatalf("Windows 默认路径不正确：%+v", paths)
	}
}

func TestReadInstalledCertificateRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("安装路径记录仅用于 Linux")
	}
	path := filepath.Join(t.TempDir(), "installation.json")
	if err := os.WriteFile(path, []byte(`{
  "certificate_root": "/data/scert/ssl"
}
`), 0o600); err != nil {
		t.Fatalf("写入安装路径记录失败：%v", err)
	}
	root, err := readInstalledCertificateRoot(path)
	if err != nil {
		t.Fatalf("读取安装路径记录失败：%v", err)
	}
	if root != filepath.Clean("/data/scert/ssl") {
		t.Fatalf("证书目录读取结果不正确：%s", root)
	}
}

func TestReadInstalledCertificateRootRejectsUnsafeValue(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("安装路径记录仅用于 Linux")
	}
	path := filepath.Join(t.TempDir(), "installation.json")
	if err := os.WriteFile(path, []byte(`{"certificate_root":"/"}`), 0o600); err != nil {
		t.Fatalf("写入安装路径记录失败：%v", err)
	}
	if _, err := readInstalledCertificateRoot(path); err == nil {
		t.Fatal("根目录不得作为安装记录中的证书目录")
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	cfg := Default(dir)
	path := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("保存配置失败：%v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("读取配置失败：%v", err)
	}
	if loaded.Version != cfg.Version || loaded.Database.Driver != "sqlite" {
		t.Fatalf("读取配置内容不一致：%+v", loaded)
	}
}

func TestValidateRejectsInvalidPort(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Server.Port = 70000
	if err := cfg.Validate(); err == nil {
		t.Fatal("无效端口未被拒绝")
	}
}

func TestResetURLUsesWebHostnameWithoutPort(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Web.Hostname = "scert.example.test"
	cfg.Server.Port = 8443
	if value := cfg.ResetURL(); value != "https://scert.example.test/reset" {
		t.Fatalf("域名密码重置地址不正确：%s", value)
	}
	cfg.Server.Port = 443
	if value := cfg.ResetURL(); value != "https://scert.example.test/reset" {
		t.Fatalf("默认 HTTPS 端口不应显示端口号：%s", value)
	}
}

func TestResetURLSupportsPublicIPv6(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Web.PublicIP = "2001:db8::10"
	cfg.Server.Port = 8443
	if value := cfg.ResetURL(); value != "https://[2001:db8::10]/reset" {
		t.Fatalf("IPv6 密码重置地址不正确：%s", value)
	}
}

func TestSavedConfigDoesNotContainIndependentResetPort(t *testing.T) {
	dir := t.TempDir()
	cfg := Default(dir)
	path := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("保存配置失败：%v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取配置失败：%v", err)
	}
	content := string(data)
	if strings.Contains(content, "reset_service_port") || strings.Contains(content, "reset_service_host") {
		t.Fatalf("配置文件不应再包含独立密码重置监听配置：%s", content)
	}
}
