package app

import (
	"context"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestNewWiresApplicationServices(t *testing.T) {
	baseDir := t.TempDir()
	cfg := config.Default(baseDir)
	application, err := NewWithConfigPath(context.Background(), cfg, filepath.Join(cfg.Paths.Config, "scert.json"))
	if err != nil {
		t.Fatalf("初始化应用失败：%v", err)
	}
	defer application.Close()

	if application.Repository == nil || application.Servers == nil || application.Tasks == nil {
		t.Fatal("数据库仓储未完整注入应用实例")
	}
	if application.Deployment == nil || application.Certificates == nil || application.Workflow == nil || application.Renewal == nil {
		t.Fatal("证书申请、部署或续期服务未完整注入应用实例")
	}
	if application.SecretStore == nil || application.SSHKeys == nil || application.DNSVerification == nil || application.HTTP == nil {
		t.Fatal("敏感信息、SSH、DNS 或 HTTP 服务未完整注入应用实例")
	}
}
