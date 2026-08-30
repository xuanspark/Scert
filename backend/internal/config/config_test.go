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

func TestResetURLAlwaysUsesWebHostnameWithoutPort(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Web.Hostname = "scert.example.test"
	cfg.Server.Port = 8443
	if value := cfg.ResetURL(); value != "https://scert.example.test/reset" {
		t.Fatalf("密码重置地址不得暴露主服务端口：%s", value)
	}
	cfg.Server.Port = 443
	if value := cfg.ResetURL(); value != "https://scert.example.test/reset" {
		t.Fatalf("默认 HTTPS 端口不应显示端口号：%s", value)
	}
}

func TestResetURLSupportsPublicIPv6WithoutPort(t *testing.T) {
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

func TestResetURLIgnoresExplicitPortAndURLInput(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Web.Hostname = "https://scert.example.test:9443"
	cfg.Server.Port = 5200
	if value := cfg.ResetURL(); value != "https://scert.example.test/reset" {
		t.Fatalf("外部地址中的显式端口不得出现在重置地址中：%s", value)
	}
}

func TestResetURLIgnoresPathFromWebHostname(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Web.Hostname = "scert.example.test/manage"
	cfg.Server.Port = 8443
	if value := cfg.ResetURL(); value != "https://scert.example.test/reset" {
		t.Fatalf("管理页面域名附带路径时重置地址不正确：%s", value)
	}
}

func TestResetURLUsesExternalProxyAddress(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Web.Hostname = "scert.example.test"
	cfg.Server.HTTPSMode = "proxy"
	cfg.Server.Port = 5200
	if value := cfg.ResetURL(); value != "https://scert.example.test/reset" {
		t.Fatalf("代理模式不应暴露核心服务端口：%s", value)
	}
}

func TestResetURLSupportsIPv6WithoutPort(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Web.PublicIP = "2001:db8::10"
	cfg.Server.Port = 443
	if value := cfg.ResetURL(); value != "https://[2001:db8::10]/reset" {
		t.Fatalf("IPv6 默认 HTTPS 地址不正确：%s", value)
	}
}

func TestDefaultACMEPropagationSettings(t *testing.T) {
	cfg := Default(t.TempDir())
	if cfg.ACME.PropagationTimeoutSecs != 900 || cfg.ACME.PropagationPollingIntervalSecs != 10 {
		t.Fatalf("默认 DNS 传播参数不正确：%+v", cfg.ACME)
	}
}

func TestNormalizeRestoresMissingACMEPropagationSettings(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.ACME.PropagationTimeoutSecs = 0
	cfg.ACME.PropagationPollingIntervalSecs = 0
	cfg.Normalize()
	if cfg.ACME.PropagationTimeoutSecs != 900 || cfg.ACME.PropagationPollingIntervalSecs != 10 {
		t.Fatalf("旧配置的 DNS 传播参数未补齐：%+v", cfg.ACME)
	}
}

func TestValidateRejectsInvalidACMEPropagationTimeout(t *testing.T) {
	for _, value := range []int{0, 86401} {
		cfg := Default(t.TempDir())
		cfg.ACME.PropagationTimeoutSecs = value
		if err := cfg.Validate(); err == nil {
			t.Fatalf("无效 DNS 传播等待时间未被拒绝：%d", value)
		}
	}
}

func TestValidateRejectsInvalidACMEPropagationInterval(t *testing.T) {
	for _, value := range []int{0, 901} {
		cfg := Default(t.TempDir())
		cfg.ACME.PropagationPollingIntervalSecs = value
		if err := cfg.Validate(); err == nil {
			t.Fatalf("无效 DNS 传播检测间隔未被拒绝：%d", value)
		}
	}
}

func TestSaveAndLoadACMEPropagationSettings(t *testing.T) {
	dir := t.TempDir()
	cfg := Default(dir)
	cfg.ACME.PropagationTimeoutSecs = 1200
	cfg.ACME.PropagationPollingIntervalSecs = 15
	path := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("保存配置失败：%v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("读取配置失败：%v", err)
	}
	if loaded.ACME.PropagationTimeoutSecs != 1200 || loaded.ACME.PropagationPollingIntervalSecs != 15 {
		t.Fatalf("DNS 传播参数持久化不一致：%+v", loaded.ACME)
	}
}

func TestLoadWithEnvOverridesRuntimeSettings(t *testing.T) {
	dir := t.TempDir()
	cfg := Default(dir)
	path := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("保存配置失败：%v", err)
	}
	t.Setenv("SCERT_SERVER_HOST", "127.0.0.1")
	t.Setenv("SCERT_SERVER_PORT", "9004")
	t.Setenv("SCERT_SERVER_HTTPS_MODE", "proxy")
	t.Setenv("SCERT_WEB_PUBLIC_IP", "192.168.2.3")
	t.Setenv("SCERT_ACME_PROPAGATION_TIMEOUT_SECS", "1200")
	loaded, err := LoadWithEnv(path)
	if err != nil {
		t.Fatalf("带环境覆盖读取配置失败：%v", err)
	}
	if loaded.Server.Host != "127.0.0.1" || loaded.Server.Port != 9004 || loaded.Server.HTTPSMode != "proxy" {
		t.Fatalf("服务运行配置覆盖不正确：%+v", loaded.Server)
	}
	if loaded.Web.PublicIP != "192.168.2.3" || loaded.ACME.PropagationTimeoutSecs != 1200 {
		t.Fatalf("运行时辅助配置覆盖不正确：web=%+v acme=%+v", loaded.Web, loaded.ACME)
	}
	fileConfig, err := Load(path)
	if err != nil {
		t.Fatalf("重新读取原配置失败：%v", err)
	}
	if fileConfig.Server.Port != 8080 || fileConfig.Web.PublicIP != "" {
		t.Fatalf("环境覆盖不应回写配置文件：%+v", fileConfig)
	}
}

func TestLoadWithEnvRejectsInvalidInteger(t *testing.T) {
	dir := t.TempDir()
	cfg := Default(dir)
	path := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("保存配置失败：%v", err)
	}
	t.Setenv("SCERT_SERVER_PORT", "九千零四")
	if _, err := LoadWithEnv(path); err == nil {
		t.Fatal("无效端口环境变量未被拒绝")
	}
}
