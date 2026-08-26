package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateRejectsIncompatibleVersion(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Version = CurrentVersion + 1
	if err := cfg.Validate(); err == nil {
		t.Fatal("不兼容配置版本未被拒绝")
	}
}

func TestSaveCanReplaceExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "scert.json")
	cfg := Default(dir)
	if err := cfg.Save(path); err != nil {
		t.Fatalf("首次保存配置失败：%v", err)
	}
	cfg.Server.Port = 9443
	if err := cfg.Save(path); err != nil {
		t.Fatalf("覆盖保存配置失败：%v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("读取覆盖后的配置失败：%v", err)
	}
	if loaded.Server.Port != 9443 {
		t.Fatalf("覆盖后的配置内容不正确：%d", loaded.Server.Port)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("读取配置文件权限失败：%v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("配置文件权限不正确：%o", info.Mode().Perm())
		}
	}
}
