package bootstrap

import (
	"context"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestCompleterPreservesExternalProxyPort(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	request := fixture.request("admin.example.test")
	request.HTTPSMode = "proxy"
	request.ServerPort = 9004
	request.ProxyHTTPSPort = 8004
	result, err := fixture.completer(nil).Complete(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ManagementURL != "https://admin.example.test:8004/" {
		t.Fatalf("初始化结果未保留代理端口：%s", result.ManagementURL)
	}
	cfg, err := config.Load(fixture.configPath)
	if err != nil || cfg.Server.ProxyHTTPSPort != 8004 || cfg.Server.Port != 9004 {
		t.Fatalf("保存代理端口失败：%v", err)
	}
}
