package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
)

type cliFakeFirewallManager struct {
	result       firewall.Result
	openErr      error
	rollbackErr  error
	openedPort   int
	rollbackCall bool
}

func (f *cliFakeFirewallManager) OpenTCP(_ context.Context, port int) (firewall.Result, error) {
	f.openedPort = port
	if f.result.Port == 0 {
		f.result.Port = port
	}
	return f.result, f.openErr
}

func (f *cliFakeFirewallManager) Rollback(_ context.Context, _ firewall.Result) error {
	f.rollbackCall = true
	return f.rollbackErr
}

func TestHelpIsLocalizedAndHasNoDuplicateCommands(t *testing.T) {
	configPath = ""
	baseDir = ""
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("执行帮助命令失败：%v", err)
	}
	text := output.String()
	for _, expected := range []string{"用法：", "可用命令：", "参数："} {
		if !strings.Contains(text, expected) {
			t.Fatalf("帮助缺少中文标题 %q：%s", expected, text)
		}
	}
	for _, unexpected := range []string{"Available Commands:", "Usage:", "Flags:", "completion"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("帮助仍包含未本地化或禁用内容 %q：%s", unexpected, text)
		}
	}
	if strings.Count(text, "\n  help ") != 1 {
		t.Fatalf("help 命令数量不正确：%s", text)
	}
}

func TestHelpSupportsSubcommand(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"help", "domain"})
	if err := root.Execute(); err != nil {
		t.Fatalf("查看子命令帮助失败：%v", err)
	}
	text := output.String()
	if !strings.Contains(text, "管理托管域名") || !strings.Contains(text, "添加托管域名") {
		t.Fatalf("子命令帮助内容不正确：%s", text)
	}
}

func TestHelpIncludesServerManagementCommands(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"help", "server"})
	if err := root.Execute(); err != nil {
		t.Fatalf("查看服务器管理帮助失败：%v", err)
	}
	text := output.String()
	for _, expected := range []string{"管理部署服务器", "添加部署服务器", "编辑部署服务器", "删除部署服务器"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("服务器管理帮助缺少 %q：%s", expected, text)
		}
	}
}

func TestServerCommandUsesDefaultCertificatePathTemplates(t *testing.T) {
	command := newServerAddCommand()
	tests := map[string]string{
		"certificate-path": deployment.DefaultCertificatePath,
		"chain-path":       deployment.DefaultCertificateChainPath,
		"private-key-path": deployment.DefaultPrivateKeyPath,
	}
	for name, expected := range tests {
		flag := command.Flag(name)
		if flag == nil || flag.DefValue != expected {
			t.Fatalf("服务器命令参数 %s 的默认值错误：%v", name, flag)
		}
		if len(flag.Annotations) != 0 {
			t.Fatalf("服务器路径参数 %s 不应强制填写：%v", name, flag.Annotations)
		}
	}
}

func TestHelpIncludesSSHKeyManagementCommands(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"help", "ssh-key"})
	if err := root.Execute(); err != nil {
		t.Fatalf("查看 SSH 私钥管理帮助失败：%v", err)
	}
	text := output.String()
	for _, expected := range []string{"管理 SSH 部署私钥", "安全录入 SSH 私钥", "删除未被服务器使用的 SSH 私钥"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("SSH 私钥管理帮助缺少 %q：%s", expected, text)
		}
	}
}

func TestCommandStatusUsesApplicationVersion(t *testing.T) {
	cfg := config.Default(t.TempDir())
	status := commandStatus(cfg)
	if status["version"] != buildinfo.Version || status["config_version"] != config.CurrentVersion {
		t.Fatalf("状态版本字段不正确：%+v", status)
	}
}

func TestSaveSetupWithFirewallOpensOnlyMainPort(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Server.Port = 8443
	manager := &cliFakeFirewallManager{result: firewall.Result{
		Backend: "windows", RuleName: "Scert Web TCP 8443", Created: true, Message: "已创建测试规则",
	}}
	path := filepath.Join(t.TempDir(), "config", "scert.json")
	result, err := saveSetupWithFirewall(context.Background(), cfg, path, manager)
	if err != nil {
		t.Fatalf("保存初始化配置失败：%v", err)
	}
	if manager.openedPort != 8443 || manager.rollbackCall || !result.Created {
		t.Fatalf("CLI 防火墙事务不正确：port=%d rollback=%t result=%+v", manager.openedPort, manager.rollbackCall, result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("初始化配置未保存：%v", err)
	}
}

func TestInitCommandDoesNotExposeResetServicePort(t *testing.T) {
	command := newInitCommandWithFirewall(&cliFakeFirewallManager{})
	if command.Flag("reset-service-port") != nil {
		t.Fatal("初始化命令不应再提供独立密码重置端口")
	}
}

func TestSaveSetupWithFirewallRollsBackNewRuleOnSaveFailure(t *testing.T) {
	baseDir := t.TempDir()
	blockedParent := filepath.Join(baseDir, "blocked")
	if err := os.WriteFile(blockedParent, []byte("占位"), 0o600); err != nil {
		t.Fatalf("创建阻塞路径失败：%v", err)
	}
	manager := &cliFakeFirewallManager{result: firewall.Result{
		Backend: "ufw", RuleName: "Scert Web TCP 8080", Port: 8080, Created: true,
	}}
	_, err := saveSetupWithFirewall(context.Background(), config.Default(baseDir), filepath.Join(blockedParent, "scert.json"), manager)
	if err == nil {
		t.Fatal("配置保存失败应返回错误")
	}
	if !manager.rollbackCall {
		t.Fatal("配置保存失败时应回滚本次新建的防火墙规则")
	}
}

func TestSaveSetupWithFirewallReportsRollbackFailure(t *testing.T) {
	baseDir := t.TempDir()
	blockedParent := filepath.Join(baseDir, "blocked")
	if err := os.WriteFile(blockedParent, []byte("占位"), 0o600); err != nil {
		t.Fatalf("创建阻塞路径失败：%v", err)
	}
	manager := &cliFakeFirewallManager{
		result:      firewall.Result{Backend: "ufw", RuleName: "Scert Web TCP 8080", Port: 8080, Created: true},
		rollbackErr: errors.New("rollback denied"),
	}
	_, err := saveSetupWithFirewall(context.Background(), config.Default(baseDir), filepath.Join(blockedParent, "scert.json"), manager)
	if err == nil || !strings.Contains(err.Error(), "手动检查本机防火墙") {
		t.Fatalf("防火墙回滚失败应返回明确错误：%v", err)
	}
}
