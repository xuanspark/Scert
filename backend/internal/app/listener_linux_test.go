package app

import (
	"context"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestNewRejectsNonLoopbackBeforeCreatingResources(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.Server.Host = "::"
	application, err := NewWithConfigPath(context.Background(), cfg, "")
	if application != nil || err == nil || !strings.Contains(err.Error(), "只允许监听回环") {
		t.Fatalf("正式服务入口未在创建资源前拒绝非回环监听：%v", err)
	}
}
