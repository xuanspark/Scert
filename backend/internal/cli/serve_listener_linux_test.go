package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestLoadServeConfigWithoutListenerOverride(t *testing.T) {
	t.Setenv("SCERT_SERVER_HOST", "")
	if err := os.Unsetenv("SCERT_SERVER_HOST"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "scert.json")
	cfg := config.Default(root)
	loaded, err := loadServeConfig(path, cfg)
	if err != nil || loaded.Server.Host != "127.0.0.1" {
		t.Fatalf("未配置环境变量时默认监听不安全：%v", err)
	}
	cfg.Server.Host = "0.0.0.0"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServeConfig(path, cfg); err == nil {
		t.Fatal("没有安全环境覆盖的历史通配监听未被拒绝")
	}
}

func TestLoadServeConfigEnforcesEffectiveLoopback(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, host := range []string{"127.0.0.1", "::1", "0.0.0.0", "::", "192.0.2.1"} {
			t.Run(host+map[bool]string{false: "/new", true: "/existing"}[existing], func(t *testing.T) {
				root := t.TempDir()
				cfg := config.Default(root)
				cfg.Server.Host = "0.0.0.0"
				path := filepath.Join(root, "scert.json")
				if existing {
					data, err := json.Marshal(cfg)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("SCERT_SERVER_HOST", host)
				loaded, err := loadServeConfig(path, cfg)
				allowed := host == "127.0.0.1" || host == "::1"
				if allowed && (err != nil || loaded.Server.Host != host) {
					t.Fatalf("回环环境覆盖未生效：%v", err)
				}
				if !allowed && err == nil {
					t.Fatal("环境覆盖后的非回环地址未被拒绝")
				}
			})
		}
	}
}
