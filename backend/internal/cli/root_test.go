package cli

import (
	"bufio"
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
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
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
	root.SetArgs([]string{"help", "certificate"})
	if err := root.Execute(); err != nil {
		t.Fatalf("查看子命令帮助失败：%v", err)
	}
	text := output.String()
	if !strings.Contains(text, "查询和校验证书") || !strings.Contains(text, "校验当前证书") {
		t.Fatalf("子命令帮助内容不正确：%s", text)
	}
}

func TestHelpIncludesSystemServiceCommands(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"help", "service"})
	if err := root.Execute(); err != nil {
		t.Fatalf("查看系统服务帮助失败：%v", err)
	}
	text := output.String()
	for _, expected := range []string{"管理系统服务", "注册 Scert 系统服务", "取消注册 Scert 系统服务"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("系统服务帮助缺少 %q：%s", expected, text)
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

func TestHelpIncludesDNSDiagnosticCommand(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"help", "dns"})
	if err := root.Execute(); err != nil {
		t.Fatalf("查看 DNS 诊断帮助失败：%v", err)
	}
	text := output.String()
	for _, expected := range []string{"检查指定域名的阿里云 RAM 权限", "验证当前权限"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("DNS 诊断帮助缺少 %q：%s", expected, text)
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

func TestAskDNSProviderUsesNumberedSelection(t *testing.T) {
	var output bytes.Buffer
	provider, err := askDNSProvider(bufio.NewReader(strings.NewReader("99\n2\n")), &output)
	if err != nil {
		t.Fatalf("DNS 服务商选择失败：%v", err)
	}
	if provider != "aliyun" {
		t.Fatalf("编号 2 应选择阿里云 DNS，实际为：%s", provider)
	}
	if !strings.Contains(output.String(), "1. Cloudflare") || !strings.Contains(output.String(), "2. 阿里云 DNS") {
		t.Fatalf("DNS 服务商列表未按编号显示：%s", output.String())
	}
	if !strings.Contains(output.String(), "编号无效，请重新输入。") {
		t.Fatalf("无效编号未提示重新输入：%s", output.String())
	}
}

func TestAskDNSProviderRejectsUnavailableSelection(t *testing.T) {
	var output bytes.Buffer
	provider, err := askDNSProvider(bufio.NewReader(strings.NewReader("9\n1\n")), &output)
	if err != nil {
		t.Fatalf("DNS 服务商选择失败：%v", err)
	}
	if provider != "cloudflare" {
		t.Fatalf("重新选择后应返回 Cloudflare，实际为：%s", provider)
	}
	if !strings.Contains(output.String(), "当前暂未开放") {
		t.Fatalf("选择未开放服务商时未给出提示：%s", output.String())
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

type cliFakeDNSCredentialTester struct {
	credentials map[string]string
}

func (f *cliFakeDNSCredentialTester) Verify(_ context.Context, _ string, _ string, credentials map[string]string) (setup.DNSVerificationReport, error) {
	f.credentials = credentials
	return setup.DNSVerificationReport{}, nil
}

func TestPrepareDNSCredentialsEncryptsAndClearsInput(t *testing.T) {
	configDir := t.TempDir()
	store, err := secretstore.Open(configDir)
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	tester := &cliFakeDNSCredentialTester{}
	manager := setup.NewDNSVerificationManager(tester, store)
	input := &setup.Input{
		DNSProvider:    dnsprovider.Cloudflare,
		DNSCredentials: map[string]string{"api_token": "test-dns-token"},
		DNSTestDomain:  "example.com",
	}
	var output bytes.Buffer
	cleanup, err := prepareDNSCredentialsWithManager(context.Background(), input, &output, manager, store)
	if err != nil {
		t.Fatalf("准备 DNS 凭据失败：%v", err)
	}
	if input.DNSCredentialRef == "" || !input.DNSVerified || input.DNSCredentials != nil {
		t.Fatalf("DNS 凭据输入状态不正确：%+v", input)
	}
	if tester.credentials["api_token"] != "test-dns-token" {
		t.Fatalf("在线验证未收到凭据：%v", tester.credentials)
	}
	content, err := os.ReadFile(filepath.Join(configDir, "secrets.json"))
	if err != nil {
		t.Fatalf("读取敏感信息文件失败：%v", err)
	}
	if strings.Contains(string(content), "test-dns-token") || strings.Contains(output.String(), "test-dns-token") {
		t.Fatal("DNS 凭据不应以明文写入文件或输出")
	}
	value, err := store.Get(context.Background(), input.DNSCredentialRef)
	if err != nil || value["api_token"] != "test-dns-token" {
		t.Fatalf("加密凭据解密结果不正确：value=%v err=%v", value, err)
	}
	cleanup()
	exists, err := store.Has(context.Background(), input.DNSCredentialRef)
	if err != nil || exists {
		t.Fatalf("保存失败清理后凭据引用仍存在：exists=%t err=%v", exists, err)
	}
}
