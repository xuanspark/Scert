package cli

import (
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestTailLinesReturnsOnlyLatestEntries(t *testing.T) {
	values, err := tailLines(strings.NewReader("one\ntwo\nthree\n"), 2)
	if err != nil {
		t.Fatalf("读取日志尾部失败：%v", err)
	}
	if strings.Join(values, ",") != "two,three" {
		t.Fatalf("日志尾部不正确：%v", values)
	}
}

func TestSafeConfigMapRedactsPasswordHash(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Admin.PasswordHash = "secret-hash"
	result := safeConfigMap(cfg)
	admin, ok := result["admin"].(map[string]any)
	if !ok {
		t.Fatalf("脱敏配置缺少管理员字段：%v", result)
	}
	if admin["password_hash"] != "已配置" {
		t.Fatalf("密码摘要未脱敏：%v", admin["password_hash"])
	}
}

func TestSetConfigValueUpdatesAndValidatesAllowedFields(t *testing.T) {
	cfg := config.Default(t.TempDir())
	if err := setConfigValue(&cfg, "server.port", "5200"); err != nil {
		t.Fatalf("设置端口失败：%v", err)
	}
	if cfg.Server.Port != 5200 {
		t.Fatalf("端口未更新：%d", cfg.Server.Port)
	}
	if err := setConfigValue(&cfg, "notification.recipients", "a@example.com, b@example.com"); err != nil {
		t.Fatalf("设置收件人失败：%v", err)
	}
	if len(cfg.Notification.Recipients) != 2 {
		t.Fatalf("收件人未拆分：%v", cfg.Notification.Recipients)
	}
	if err := setConfigValue(&cfg, "notification.http_login_path", "/api/login"); err != nil {
		t.Fatalf("设置 HTTP 登录路径失败：%v", err)
	}
	if cfg.Notification.HTTPLoginPath != "/api/login" {
		t.Fatalf("HTTP 登录路径未更新：%s", cfg.Notification.HTTPLoginPath)
	}
	if err := setConfigValue(&cfg, "acme.propagation_timeout_secs", "1200"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "acme.propagation_polling_interval_secs", "15"); err != nil {
		t.Fatal(err)
	}
	if cfg.ACME.PropagationTimeoutSecs != 1200 || cfg.ACME.PropagationPollingIntervalSecs != 15 {
		t.Fatalf("DNS 传播参数未更新：%+v", cfg.ACME)
	}
	if err := setConfigValue(&cfg, "admin.password_hash", "plain"); err == nil {
		t.Fatal("不应允许直接修改密码摘要")
	}
}

func TestRootIncludesOperationalCommands(t *testing.T) {
	root := newRootCommand()
	for _, name := range []string{"certificate", "renew", "deploy", "logs", "config", "backup", "restore"} {
		if root.CommandPath() == name || root.Commands() == nil {
			t.Fatalf("根命令状态异常：%s", name)
		}
		found := false
		for _, command := range root.Commands() {
			if command.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("缺少命令：%s", name)
		}
	}
}
