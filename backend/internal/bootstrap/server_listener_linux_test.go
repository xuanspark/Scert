package bootstrap

import (
	"context"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestRunRejectsNonLoopbackBeforeInitialization(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Server.Host = "0.0.0.0"
	completed, err := Run(context.Background(), ServerOptions{Config: cfg})
	if completed || err == nil || !strings.Contains(err.Error(), "只允许监听回环") {
		t.Fatalf("初始化入口未在创建资源前拒绝非回环监听：%v", err)
	}
}
