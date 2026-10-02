package config

import (
	"path/filepath"
	"testing"
)

func TestProxyHTTPSPortValidationAndPersistence(t *testing.T) {
	for _, port := range []int{-1, 65536} {
		cfg := Default(t.TempDir())
		cfg.Server.ProxyHTTPSPort = port
		if err := cfg.Validate(); err == nil {
			t.Fatalf("非法代理端口未被拒绝：%d", port)
		}
	}
	for _, port := range []int{0, 1, 443, 8004, 65535} {
		cfg := Default(t.TempDir())
		cfg.Server.HTTPSMode = "proxy"
		cfg.Server.ProxyHTTPSPort = port
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "scert.json")
		if err := cfg.Save(path); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path)
		if err != nil || loaded.Server.ProxyHTTPSPort != port {
			t.Fatalf("代理端口保存失败：%d，%v", port, err)
		}
	}
}

func TestProxyHTTPSPortEnvironment(t *testing.T) {
	t.Setenv("SCERT_SERVER_PROXY_HTTPS_PORT", "8004")
	cfg := Default(t.TempDir())
	if err := cfg.ApplyEnvironment(); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ProxyHTTPSPort != 8004 {
		t.Fatal("环境变量未覆盖代理端口")
	}
}
