package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestLoadServeConfigFailsClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "conf", "scert.json")
	defaults := config.Default(root)
	cfg, err := loadServeConfig(path, defaults)
	if err != nil || cfg.SetupReady || cfg.Paths.Config != filepath.Dir(path) {
		t.Fatalf("缺失配置应进入初始化：%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("仅加载不应保存默认配置")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"{", "null", "{}", `{"setup_ready":true}`, "[]", ""} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadServeConfig(path, defaults); err == nil {
			t.Fatalf("损坏配置被降级成初始化：%q", data)
		}
	}
	if _, err := loadServeConfig(root, defaults); err == nil {
		t.Fatal("目录不应被视为缺失配置")
	}
}

func TestServeLifecycle(t *testing.T) {
	for _, name := range []string{"missing", "ready", "cancel", "bootstrap-error", "reload-error", "not-ready", "path-changed", "web-disabled", "application-error"} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Default(t.TempDir())
			cfg.SetupReady = name == "ready" || name == "web-disabled" || name == "application-error"
			cfg.Web.Enabled = name != "web-disabled"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bootstrapCalls, reloadCalls, appCalls := 0, 0, 0
			err := serveLifecycle(ctx, cfg, func() (config.Config, error) {
				reloadCalls++
				if name == "reload-error" {
					return config.Config{}, errors.New("test")
				}
				next := cfg
				next.SetupReady = name != "not-ready"
				if name == "path-changed" {
					next.Paths.Data += "-changed"
				}
				return next, nil
			}, func(context.Context, config.Config) (bool, error) {
				bootstrapCalls++
				if name == "bootstrap-error" {
					return false, errors.New("test")
				}
				if name == "cancel" {
					cancel()
					return false, nil
				}
				return true, nil
			}, func(_ context.Context, ready config.Config) (bool, error) {
				appCalls++
				if !ready.SetupReady {
					t.Fatal("未初始化时构造业务应用")
				}
				if name == "application-error" {
					return false, errors.New("test")
				}
				return false, nil
			})
			wantError := name != "missing" && name != "ready" && name != "cancel"
			if (err != nil) != wantError {
				t.Fatalf("错误边界不符：%v", err)
			}
			wantApp := 0
			if name == "missing" || name == "ready" || name == "application-error" {
				wantApp = 1
			}
			if appCalls != wantApp {
				t.Fatalf("业务启动次数：%d", appCalls)
			}
			if cfg.SetupReady && (bootstrapCalls != 0 || reloadCalls != 0) {
				t.Fatal("已完成配置进入初始化")
			}
			if !cfg.SetupReady && bootstrapCalls != 1 {
				t.Fatal("初始化未运行一次")
			}
		})
	}
}
