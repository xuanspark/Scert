package main

import (
	"context"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/adminconsole"
)

func TestAdminRejectsLegacyArguments(t *testing.T) {
	err := adminconsole.Run(context.Background(), []string{"--config", "scert.json"})
	if err == nil {
		t.Fatal("Scert-admin 不得接受历史命令行参数")
	}
	if !strings.Contains(err.Error(), "不接受参数") {
		t.Fatalf("错误信息未说明参数入口已关闭：%v", err)
	}
}
