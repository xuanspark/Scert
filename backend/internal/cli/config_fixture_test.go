package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// isolatedCLIConfig 明确覆盖 Linux 的正式安装路径，测试不得访问现有服务数据。
func isolatedCLIConfig(root string) config.Config {
	cfg := config.Default(root)
	cfg.Paths = config.PathsConfig{
		Install:     root,
		Config:      filepath.Join(root, "conf"),
		Data:        filepath.Join(root, "data"),
		Certificate: filepath.Join(root, "ssl"),
		Log:         filepath.Join(root, "log"),
		Backup:      filepath.Join(root, "backups"),
	}
	cfg.Database.DSN = filepath.Join(cfg.Paths.Data, "scert.db")
	return cfg
}

func TestCLIConfigStaysInTempRoot(t *testing.T) {
	root := t.TempDir()
	cfg := isolatedCLIConfig(root)
	for _, path := range []string{cfg.Paths.Install, cfg.Paths.Config, cfg.Paths.Data, cfg.Paths.Certificate, cfg.Paths.Log, cfg.Paths.Backup, cfg.Database.DSN} {
		relative, err := filepath.Rel(root, path)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("测试配置逃逸出临时目录：%s", path)
		}
	}
}
