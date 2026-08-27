package admin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
		"RuntimeMaxSec=30min",
		"UMask=0077",
		"ProtectHome=yes",
		"NoNewPrivileges=yes",
		"RestrictAddressFamilies=AF_UNIX",
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
		"AF_INET",
		"AF_INET6",
	} {
		if strings.Contains(unit, forbidden) {
			t.Errorf("Scert-admin systemd unit 不得包含：%s", forbidden)
		}
	}
}

func TestControllerActivatesAndStopsResetPath(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.Web.Hostname = "scert.example.test"
	cfg.Server.Port = 8443
	controller := New(cfg)
	fixedNow := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	controller.now = func() time.Time { return fixedNow }

	session, err := controller.Start()
	if err != nil {
		t.Fatalf("启用密码重置入口失败：%v", err)
	}
	if session.State.ResetURL != "https://scert.example.test/reset" || session.Code == "" {
		t.Fatalf("密码重置会话内容不正确：%+v", session)
	}
	if session.State.CodeHash == session.Code || session.State.CodeHash == "" {
		t.Fatal("状态文件只能保存一次性校验码摘要")
	}
	active, err := IsActive(StatePath(cfg.Paths.Data), fixedNow.Add(time.Second))
	if err != nil || !active {
		t.Fatalf("密码重置入口应处于激活状态：active=%t err=%v", active, err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatalf("关闭密码重置入口失败：%v", err)
	}
	active, err = IsActive(StatePath(cfg.Paths.Data), fixedNow.Add(time.Second))
	if err != nil || active {
		t.Fatalf("停止 Scert-admin 后入口必须关闭：active=%t err=%v", active, err)
	}
}

func TestControllerRequiresCompletedSetup(t *testing.T) {
	cfg := config.Default(t.TempDir())
	if _, err := New(cfg).Start(); err == nil {
		t.Fatal("Scert 未初始化时不得启用密码重置入口")
	}
}

func TestExpiredLeaseIsInactive(t *testing.T) {
	path := StatePath(t.TempDir())
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	if err := writeState(path, State{Active: true, ExpiresAt: now.Add(-time.Second)}); err != nil {
		t.Fatalf("写入过期租约失败：%v", err)
	}
	active, err := IsActive(path, now)
	if err != nil || active {
		t.Fatalf("过期租约不得保持激活：active=%t err=%v", active, err)
	}
}

func TestResetCodeCanOnlyBeConsumedOnce(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.Web.Hostname = "scert.example.test"
	controller := New(cfg)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	controller.now = func() time.Time { return now }
	session, err := controller.Start()
	if err != nil {
		t.Fatalf("启动密码重置会话失败：%v", err)
	}
	verified, err := VerifyCode(StatePath(cfg.Paths.Data), session.Code, now)
	if err != nil || verified.CodeHash != session.State.CodeHash {
		t.Fatalf("校验一次性校验码失败：state=%+v err=%v", verified, err)
	}
	if err := Consume(StatePath(cfg.Paths.Data), verified, now); err != nil {
		t.Fatalf("消费一次性校验码失败：%v", err)
	}
	if _, err := VerifyCode(StatePath(cfg.Paths.Data), session.Code, now); err != ErrResetSessionClosed {
		t.Fatalf("已消费校验码必须拒绝再次使用：%v", err)
	}
	if err := controller.Refresh(); err != ErrResetSessionClosed {
		t.Fatalf("Scert-admin 刷新已消费会话必须停止：%v", err)
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
