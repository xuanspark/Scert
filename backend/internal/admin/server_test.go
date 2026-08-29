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
		"Documentation=file:///scert/docs/Scert-admin-service-management.md",
		"User=Scert",
		"Group=Scert",
		"RuntimeDirectory=scert-admin",
		"RuntimeDirectoryMode=0700",
		"ExecStart=/scert/scert-admin --config /scert/conf/scert.json --code-output /run/scert-admin/reset-code",
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

func TestControllerWritesAndDestroysProtectedCodeFile(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.Web.Hostname = "scert.example.test"
	codePath := filepath.Join(t.TempDir(), "runtime", "reset-code")
	controller := NewWithCodePath(cfg, codePath)

	session, err := controller.Start()
	if err != nil {
		t.Fatalf("启动密码重置入口失败：%v", err)
	}
	content, err := os.ReadFile(codePath)
	if err != nil {
		t.Fatalf("读取受保护校验码文件失败：%v", err)
	}
	if string(content) != session.Code+"\n" {
		t.Fatalf("校验码文件内容错误：%q", string(content))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(codePath)
		if err != nil {
			t.Fatalf("读取校验码文件属性失败：%v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("校验码文件权限错误：%o", info.Mode().Perm())
		}
	}
	if err := controller.Stop(); err != nil {
		t.Fatalf("停止密码重置入口失败：%v", err)
	}
	if _, err := os.Stat(codePath); !os.IsNotExist(err) {
		t.Fatalf("停止服务后校验码文件必须销毁，当前错误：%v", err)
	}
}

func TestConsumeDestroysCodeFile(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.Web.Hostname = "scert.example.test"
	codePath := filepath.Join(t.TempDir(), "runtime", "reset-code")
	controller := NewWithCodePath(cfg, codePath)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	controller.now = func() time.Time { return now }

	session, err := controller.Start()
	if err != nil {
		t.Fatalf("启动密码重置入口失败：%v", err)
	}
	verified, err := VerifyCode(StatePath(cfg.Paths.Data), session.Code, now)
	if err != nil {
		t.Fatalf("校验一次性校验码失败：%v", err)
	}
	if err := Consume(StatePath(cfg.Paths.Data), verified, now); err != nil {
		t.Fatalf("消费一次性校验码失败：%v", err)
	}
	if _, err := os.Stat(codePath); !os.IsNotExist(err) {
		t.Fatalf("校验码消费后文件必须销毁，当前错误：%v", err)
	}
}

func TestExpiredLeaseDestroysCodeFile(t *testing.T) {
	dataDir := t.TempDir()
	statePath := StatePath(dataDir)
	codePath := CodePath(dataDir)
	if err := writeCodeFile(codePath, "expired-code"); err != nil {
		t.Fatalf("创建过期校验码文件失败：%v", err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if err := writeState(statePath, State{Active: true, CodePath: codePath, ExpiresAt: now.Add(-time.Second)}); err != nil {
		t.Fatalf("写入过期租约失败：%v", err)
	}
	active, err := IsActive(statePath, now)
	if err != nil || active {
		t.Fatalf("过期租约必须失效：active=%t err=%v", active, err)
	}
	if _, err := os.Stat(codePath); !os.IsNotExist(err) {
		t.Fatalf("租约失效后校验码文件必须销毁，当前错误：%v", err)
	}
}

func TestControllerDoesNotOverwriteExistingCodeFile(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.Web.Hostname = "scert.example.test"
	codePath := filepath.Join(t.TempDir(), "runtime", "reset-code")
	if err := os.MkdirAll(filepath.Dir(codePath), 0o700); err != nil {
		t.Fatalf("创建测试目录失败：%v", err)
	}
	const existing = "existing-code\n"
	if err := os.WriteFile(codePath, []byte(existing), 0o600); err != nil {
		t.Fatalf("创建已有校验码文件失败：%v", err)
	}
	if _, err := NewWithCodePath(cfg, codePath).Start(); err == nil {
		t.Fatal("已有校验码文件时不允许覆盖")
	}
	content, err := os.ReadFile(codePath)
	if err != nil {
		t.Fatalf("读取已有校验码文件失败：%v", err)
	}
	if string(content) != existing {
		t.Fatalf("已有校验码文件被修改：%q", string(content))
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

func TestConsumeExpiredSessionRemovesCodeFile(t *testing.T) {
	baseDir := t.TempDir()
	statePath := filepath.Join(baseDir, "state.json")
	codePath := filepath.Join(baseDir, "reset-code")
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if err := os.WriteFile(codePath, []byte("expired-code\n"), 0o600); err != nil {
		t.Fatalf("创建过期校验码文件失败：%v", err)
	}
	state := State{
		Active:    true,
		CodeHash:  hashCode("expired-code"),
		CodePath:  codePath,
		ExpiresAt: now.Add(-time.Second),
	}
	if err := writeState(statePath, state); err != nil {
		t.Fatalf("写入过期会话失败：%v", err)
	}
	if err := Consume(statePath, state, now); err != ErrResetSessionClosed {
		t.Fatalf("消费过期会话应返回关闭错误：%v", err)
	}
	if _, err := os.Stat(codePath); !os.IsNotExist(err) {
		t.Fatalf("消费过期会话后必须销毁校验码文件：%v", err)
	}
}
