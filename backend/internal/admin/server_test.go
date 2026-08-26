package admin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestScertAdminSystemdUnitKeepsManualOnlyPolicy(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试源文件")
	}
	unitPath := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "deploy", "systemd", "Scert-admin.service")
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("读取 Scert-admin systemd unit 失败：%v", err)
	}
	unit := activeUnitText(string(data))

	for _, required := range []string{
		"Documentation=file:///scert/docs/Scert-admin服务管理说明.md",
		"User=Scert",
		"Group=Scert",
		"ExecStart=/scert/scert-admin --config /scert/conf/scert.json",
		"Restart=no",
		"UMask=0077",
		"ProtectHome=yes",
		"NoNewPrivileges=yes",
	} {
		if !strings.Contains(unit, required) {
			t.Errorf("Scert-admin systemd unit 缺少必要策略：%s", required)
		}
	}
	for _, forbidden := range []string{
		"[Install]",
		"WantedBy=",
		"Restart=always",
		"Restart=on-failure",
		"ExecReload=",
	} {
		if strings.Contains(unit, forbidden) {
			t.Errorf("Scert-admin systemd unit 不得包含：%s", forbidden)
		}
	}
}

func TestListenAndServeRejectsNonLoopbackHost(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Admin.ResetServiceHost = "0.0.0.0"
	server := New(cfg)
	if err := server.ListenAndServe(); err == nil {
		t.Fatal("Scert-admin 必须拒绝非回环监听地址")
	}
}

func TestConfigRejectsLocalhostAlias(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Admin.ResetServiceHost = "localhost"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Scert-admin 不得依赖 localhost 名称解析")
	}
}

func activeUnitText(unit string) string {
	lines := strings.Split(unit, "\n")
	active := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		active = append(active, trimmed)
	}
	return strings.Join(active, "\n")
}
