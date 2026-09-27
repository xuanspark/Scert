package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"git.pcux.cn/xuans/Scert/backend/internal/app"
	"git.pcux.cn/xuans/Scert/backend/internal/bootstrap"
	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

const version = buildinfo.Version

var (
	configPath string
	baseDir    string
)

const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}{{end}}

用法：
  {{.UseLine}}{{if .HasAvailableSubCommands}}

可用命令：
{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}  {{rpad .Name .NamePadding }} {{.Short}}{{"\n"}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

参数：
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

全局参数：
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

其他帮助：
  {{.CommandPath}} help [命令]{{end}}
`

func Execute() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{Use: "scert", Short: "SSL 证书服务与运维工具，业务管理请使用 Web", SilenceUsage: true}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(newHelpCommand())
	root.SetHelpTemplate(helpTemplate)
	root.PersistentFlags().StringVar(&configPath, "config", "", "配置文件路径")
	root.PersistentFlags().StringVar(&baseDir, "base-dir", "", "Windows 默认安装根目录")
	root.AddCommand(
		&cobra.Command{Use: "version", Short: "显示版本信息", Run: func(cmd *cobra.Command, args []string) { cmd.Printf("Scert %s\n", version) }},
		newInitCommand(), newStatusCommand(), newServeCommand(), newCertificateCommand(),
		newLogsCommand(), newConfigCommand(), newBackupCommand(), newRestoreCommand(),
		newServiceCommand(), newDNSCommand(),
	)
	for _, name := range []string{"domain", "server", "ssh-key", "renew", "deploy"} {
		root.AddCommand(newRetiredBusinessCommand(name))
	}
	localizeHelpFlags(root)
	return root
}

// 历史业务命令只保留迁移提示，不读取配置、凭据或数据库，也不执行远程操作。
func newRetiredBusinessCommand(name string) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		Short:              "业务命令已迁移至 Web 管理页面",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("此业务命令已停用，请使用 Web 管理页面；CLI 仅保留初始化、服务运行和运维诊断、数据库维护、备份恢复及系统服务管理")
		},
	}
}

func newHelpCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "help [命令]",
		Short: "显示帮助信息",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Root().Help()
			}
			target, remaining, err := cmd.Root().Find(args)
			if err != nil {
				return err
			}
			if len(remaining) > 0 {
				return fmt.Errorf("未知命令：%s", strings.Join(remaining, " "))
			}
			return target.Help()
		},
	}
}

func localizeHelpFlags(command *cobra.Command) {
	command.DisableFlagsInUseLine = true
	command.InitDefaultHelpFlag()
	if flag := command.Flags().Lookup("help"); flag != nil {
		flag.Usage = "显示帮助信息"
	}
	for _, child := range command.Commands() {
		localizeHelpFlags(child)
	}
}

func newInitCommand() *cobra.Command {
	return newInitCommandWithFirewall(firewall.NewManager())
}

func newInitCommandWithFirewall(firewallManager firewall.Manager) *cobra.Command {
	return newInitCommandWithDependencies(firewallManager, issueInitCertificate)
}

func newInitCommandWithDependencies(firewallManager firewall.Manager, issue initCertificateIssuer) *cobra.Command {
	var (
		adminUsername, adminEmail, adminPassword, emailProvider, emailEndpoint                         string
		emailHTTPPath, emailHTTPLoginPath, emailHTTPAuthMode, emailHTTPCredentialRef, emailHTTPHeaders string
		emailSMTPHost, emailSMTPUsername, emailSMTPPassword, emailSMTPMode                             string
		emailEventMode, emailSenderName, emailSenderEmail, emailRetryIntervals                         string
		recipients, dnsProvider, dnsCredentialRef, certificateRoot                                     string
		webHostname, webPublicIP, httpsMode, acmeEnvironment                                           string
		serverPort, emailTimeoutSecs, emailRetryCount, emailSMTPPort                                   int
		proxyHTTPSPort                                                                                 int
		emailHTTPEnabled, emailSMTPEnabled, emailSMTPAllowPlaintextAuth, emailIncludeDeploymentDetails bool
		emailIncludeCertificateSerial, emailIncludeManagementURL                                       bool
		dnsVerified, webEnabled                                                                        bool
	)
	command := &cobra.Command{Use: "init", Short: "交互式初始化配置", RunE: func(cmd *cobra.Command, args []string) error {
		path := resolveConfigPath()
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("配置文件已存在：%s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		cfg := config.Default(baseDir)
		cfg.ACME.Environment = acmeEnvironment
		lock, err := acquireProcessLock(cfg, "初始化、服务或备份恢复任务正在执行，请稍后重试")
		if err != nil {
			return err
		}
		defer lock.Close()
		// 等待进程锁期间另一个初始化可能已完成，必须在取得锁后再次检查。
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("配置文件已存在：%s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		emailHeaders, err := parseStringMap(emailHTTPHeaders)
		if err != nil {
			return fmt.Errorf("HTTP 自定义请求头格式无效：%w", err)
		}
		emailIntervals, err := splitInts(emailRetryIntervals)
		if err != nil {
			return fmt.Errorf("通知重试间隔格式无效：%w", err)
		}
		input := setup.Input{
			AdminUsername: adminUsername, AdminEmail: adminEmail, AdminPassword: adminPassword, ServerPort: serverPort,
			EmailHTTPEnabled: emailHTTPEnabled, EmailSMTPEnabled: emailSMTPEnabled,
			EmailSMTPHost: emailSMTPHost, EmailSMTPPort: emailSMTPPort, EmailSMTPUsername: emailSMTPUsername,
			EmailSMTPPassword: emailSMTPPassword,
			EmailSMTPMode:     emailSMTPMode, EmailSMTPAllowPlaintextAuth: emailSMTPAllowPlaintextAuth,
			EmailProvider: emailProvider, EmailEndpoint: emailEndpoint,
			EmailHTTPPath: emailHTTPPath, EmailHTTPLoginPath: emailHTTPLoginPath, EmailHTTPAuthMode: emailHTTPAuthMode,
			EmailHTTPCredentialRef: emailHTTPCredentialRef, EmailHTTPHeaders: emailHeaders,
			EmailEventMode: emailEventMode, EmailIncludeDeploymentDetails: emailIncludeDeploymentDetails,
			EmailIncludeCertificateSerial: emailIncludeCertificateSerial, EmailIncludeManagementURL: emailIncludeManagementURL,
			EmailSenderName: emailSenderName, EmailSenderEmail: emailSenderEmail,
			EmailTimeoutSecs: emailTimeoutSecs, EmailRetryCount: emailRetryCount,
			EmailRetryIntervalsSecs: emailIntervals,
			EmailRecipients:         splitValues(recipients), DNSProvider: dnsProvider, DNSCredentialRef: dnsCredentialRef,
			DNSVerified: dnsVerified, CertificateRoot: certificateRoot, WebHostname: webHostname,
			WebPublicIP: webPublicIP, WebEnabled: webEnabled, HTTPSMode: httpsMode, ProxyHTTPSPort: proxyHTTPSPort,
		}
		if !cmd.Flags().Changed("admin-username") {
			if err := readInteractiveInit(cmd.InOrStdin(), cmd.OutOrStdout(), &input, cfg); err != nil {
				return err
			}
		}
		cleanupDNS, err := prepareDNSCredentials(cmd.Context(), cfg, &input, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		cleanupSMTP, err := prepareSMTPCredentials(cmd.Context(), cfg, &input)
		if err != nil {
			cleanupDNS()
			return err
		}
		cleanupSecret := func() {
			cleanupSMTP()
			cleanupDNS()
		}
		if err := setup.Apply(&cfg, input); err != nil {
			cleanupSecret()
			return err
		}
		firewallResult, err := completeInit(cmd.Context(), &cfg, path, firewallManager, issue, certificate.RollbackSavedResource)
		if err != nil {
			cleanupSecret()
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "初始化配置已保存：%s\n", path)
		fmt.Fprintln(cmd.OutOrStdout(), "管理后台证书已申请并校验，DNS 凭据已加密保存。")
		if cfg.ACME.Environment == "staging" {
			fmt.Fprintln(cmd.OutOrStdout(), "当前使用 ACME 测试环境，证书不受浏览器信任，不能作为正式生产证书。")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "本机防火墙：%s\n", firewallResult.Message)
		fmt.Fprintf(cmd.OutOrStdout(), "请确认云安全组已放行主服务端口 %d。Scert-admin 默认关闭；需要重置密码时启动后访问：%s\n", cfg.Server.Port, cfg.ResetURL())
		return nil
	}}
	command.Flags().StringVar(&adminUsername, "admin-username", "", "超级管理员用户名")
	command.Flags().StringVar(&adminEmail, "admin-email", "", "超级管理员绑定邮箱")
	command.Flags().StringVar(&adminPassword, "admin-password", "", "超级管理员密码（仅建议临时使用参数，优先交互输入）")
	command.Flags().IntVar(&serverPort, "server-port", 80, "Scert 主服务端口")
	command.Flags().IntVar(&proxyHTTPSPort, "proxy-https-port", 0, "代理公开 HTTPS 端口，0 表示默认 443")
	command.Flags().BoolVar(&emailHTTPEnabled, "email-http-enabled", false, "是否启用 HTTP 邮件通知")
	command.Flags().BoolVar(&emailSMTPEnabled, "email-smtp-enabled", false, "是否启用 SMTP 邮件通知")
	command.Flags().StringVar(&emailSMTPHost, "email-smtp-host", "", "SMTP 服务器主机")
	command.Flags().IntVar(&emailSMTPPort, "email-smtp-port", 587, "SMTP 服务器端口")
	command.Flags().StringVar(&emailSMTPUsername, "email-smtp-username", "", "SMTP 用户名")
	command.Flags().StringVar(&emailSMTPPassword, "email-smtp-password", "", "SMTP 密码（仅建议临时使用参数，优先交互输入）")
	command.Flags().StringVar(&emailSMTPMode, "email-smtp-mode", "starttls", "SMTP 连接模式：plain、starttls、implicit_tls")
	command.Flags().BoolVar(&emailSMTPAllowPlaintextAuth, "email-smtp-allow-plaintext-auth", false, "是否允许 SMTP 明文连接认证")
	command.Flags().StringVar(&emailHTTPPath, "email-http-path", "/api/email/send", "HTTP 邮件通知请求路径")
	command.Flags().StringVar(&emailHTTPLoginPath, "email-http-login-path", "/api/login", "HTTP 会话认证登录请求路径")
	command.Flags().StringVar(&emailHTTPAuthMode, "email-http-auth-mode", "none", "HTTP 邮件通知认证方式：none、bearer、hmac_sha256、session")
	command.Flags().StringVar(&emailHTTPCredentialRef, "email-http-credential-ref", "", "HTTP 邮件通知凭据引用")
	command.Flags().StringVar(&emailHTTPHeaders, "email-http-headers", "", "HTTP 自定义请求头 JSON")
	command.Flags().StringVar(&emailEventMode, "email-event-mode", "detailed", "通知事件模式：detailed、unified")
	command.Flags().StringVar(&emailSenderName, "email-sender-name", "Scert", "通知发件人名称")
	command.Flags().StringVar(&emailSenderEmail, "email-sender-email", "", "兼容通知发件人地址；Smail 系统通知账号自动确定，其他 SMTP 留空使用完整邮箱账号")
	command.Flags().BoolVar(&emailIncludeDeploymentDetails, "email-include-deployment-details", true, "是否发送服务器部署明细")
	command.Flags().BoolVar(&emailIncludeCertificateSerial, "email-include-certificate-serial", true, "是否发送证书序列号")
	command.Flags().BoolVar(&emailIncludeManagementURL, "email-include-management-url", true, "是否发送管理后台链接")
	command.Flags().StringVar(&emailProvider, "email-provider", "", "第三方邮箱服务标识")
	command.Flags().StringVar(&emailEndpoint, "email-endpoint", "", "第三方邮箱 API 地址")
	command.Flags().StringVar(&recipients, "recipients", "", "通知收件人，多个地址用逗号分隔")
	command.Flags().IntVar(&emailTimeoutSecs, "email-timeout-secs", 10, "通知请求超时时间（秒）")
	command.Flags().IntVar(&emailRetryCount, "email-retry-count", 3, "通知失败重试次数")
	command.Flags().StringVar(&emailRetryIntervals, "email-retry-intervals", "30,120,600", "通知重试间隔（秒，逗号分隔）")
	command.Flags().StringVar(&dnsProvider, "dns-provider", "", "DNS API 服务商标识")
	command.Flags().StringVar(&dnsCredentialRef, "dns-credential-ref", "", "DNS 凭据引用")
	command.Flags().BoolVar(&dnsVerified, "dns-verified", false, "兼容参数，不会跳过在线权限和证书校验")
	command.Flags().StringVar(&acmeEnvironment, "acme-environment", "staging", "ACME 环境：staging 为测试，production 为正式签发")
	command.Flags().StringVar(&certificateRoot, "certificate-root", "", "证书部署根目录，默认使用系统目录")
	command.Flags().StringVar(&webHostname, "web-hostname", "", "管理页面域名")
	command.Flags().StringVar(&webPublicIP, "web-public-ip", "", "没有域名时使用的公网 IP")
	command.Flags().BoolVar(&webEnabled, "web-enabled", true, "是否开启管理页面")
	command.Flags().StringVar(&httpsMode, "https-mode", "direct", "HTTPS 模式：direct、proxy")
	return command
}

func saveSetupWithFirewall(ctx context.Context, cfg config.Config, path string, firewallManager firewall.Manager) (firewall.Result, error) {
	result := firewall.Result{
		Backend: firewall.BackendNone, Port: cfg.Server.Port,
		Message: "未配置防火墙管理器，已跳过本机规则创建",
	}
	if firewallManager != nil {
		firewallContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		var err error
		result, err = firewallManager.OpenTCP(firewallContext, cfg.Server.Port)
		cancel()
		if err != nil {
			return firewall.Result{}, fmt.Errorf("自动配置本机防火墙失败，请使用管理员权限重新运行初始化程序：%w", err)
		}
	}
	if err := cfg.Save(path); err != nil {
		if result.Created && firewallManager != nil {
			rollbackContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			rollbackErr := firewallManager.Rollback(rollbackContext, result)
			cancel()
			if rollbackErr != nil {
				return firewall.Result{}, fmt.Errorf("保存初始化配置失败，且自动创建的防火墙规则回滚失败，请手动检查本机防火墙：保存错误：%v；回滚错误：%w", err, rollbackErr)
			}
		}
		return firewall.Result{}, err
	}
	return result, nil
}

func readInteractiveInit(input io.Reader, output io.Writer, value *setup.Input, cfg config.Config) error {
	reader := bufio.NewReader(input)
	fmt.Fprintln(output, "开始初始化 Scert。带 * 的项目为必填项。")
	var err error
	value.AdminUsername, err = ask(reader, output, "超级管理员用户名 *", "admin")
	if err != nil {
		return err
	}
	value.AdminEmail, err = ask(reader, output, "超级管理员绑定邮箱 *", "")
	if err != nil {
		return err
	}
	value.AdminPassword, err = ask(reader, output, "超级管理员密码 *", "")
	if err != nil {
		return err
	}
	port, err := askInt(reader, output, "Scert 主服务端口 *", value.ServerPort)
	if err != nil {
		return err
	}
	value.ServerPort = port
	value.EmailHTTPEnabled, err = askYesNo(reader, output, "是否启用 HTTP 邮件通知？", false)
	if err != nil {
		return err
	}
	if value.EmailHTTPEnabled {
		value.EmailProvider, err = ask(reader, output, "第三方邮箱服务标识", "")
		if err != nil {
			return err
		}
		value.EmailEndpoint, err = ask(reader, output, "第三方邮箱 API 地址", "")
		if err != nil {
			return err
		}
	}
	value.EmailSMTPEnabled, err = askYesNo(reader, output, "是否启用 SMTP 邮件通知？", false)
	if err != nil {
		return err
	}
	if value.EmailSMTPEnabled {
		value.EmailSMTPHost, err = ask(reader, output, "SMTP 服务器主机 *", "")
		if err != nil {
			return err
		}
		value.EmailSMTPPort, err = askInt(reader, output, "SMTP 服务器端口 *", 587)
		if err != nil {
			return err
		}
		value.EmailSMTPUsername, err = ask(reader, output, "SMTP 用户名 *", "")
		if err != nil {
			return err
		}
		value.EmailSMTPPassword, err = ask(reader, output, "SMTP 密码或授权码 *", "")
		if err != nil {
			return err
		}
		value.EmailSMTPMode, err = ask(reader, output, "SMTP 连接模式（plain/starttls/implicit_tls）", "starttls")
		if err != nil {
			return err
		}
		if value.EmailSMTPMode == "plain" {
			value.EmailSMTPAllowPlaintextAuth, err = askYesNo(reader, output, "是否允许 SMTP 明文认证？", false)
			if err != nil {
				return err
			}
		}
	}
	receiver, err := ask(reader, output, "通知收件人（逗号分隔）", "")
	if err != nil {
		return err
	}
	value.EmailRecipients = splitValues(receiver)
	value.DNSProvider, err = askDNSProvider(reader, output)
	if err != nil {
		return err
	}
	value.DNSCredentials, err = askDNSCredentials(reader, output, value.DNSProvider)
	if err != nil {
		return err
	}
	value.DNSTestDomain, err = ask(reader, output, "DNS 权限测试域名 *", "")
	if err != nil {
		return err
	}
	value.CertificateRoot, err = ask(reader, output, "证书部署根目录", cfg.Paths.Certificate)
	if err != nil {
		return err
	}
	value.WebHostname, err = ask(reader, output, "管理页面域名（无域名可留空）", "")
	if err != nil {
		return err
	}
	if value.WebHostname == "" {
		value.WebPublicIP, err = ask(reader, output, "管理页面公网 IP", "")
		if err != nil {
			return err
		}
	}
	value.WebEnabled, err = askYesNo(reader, output, "是否开启管理页面？", true)
	return err
}

func askDNSCredentials(reader *bufio.Reader, output io.Writer, providerID string) (map[string]string, error) {
	descriptor, err := dnsprovider.GetDescriptor(providerID)
	if err != nil {
		return nil, err
	}
	credentials := make(map[string]string, len(descriptor.CredentialFields))
	fmt.Fprintln(output, "请输入 DNS API 凭据。输入内容会在当前配置过程中明文显示，提交后只保存加密内容。")
	for _, field := range descriptor.CredentialFields {
		label := field.Label
		if field.Required {
			label += " *"
		}
		value, err := ask(reader, output, label, "")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(value) != "" {
			credentials[field.Name] = value
		}
	}
	return credentials, nil
}

func prepareSMTPCredentials(ctx context.Context, cfg config.Config, input *setup.Input) (func(), error) {
	if input == nil {
		return func() {}, errors.New("初始化输入不能为空")
	}
	if !input.EmailSMTPEnabled {
		return func() {}, nil
	}
	if strings.TrimSpace(input.EmailSMTPCredentialRef) != "" {
		return func() {}, nil
	}
	if strings.TrimSpace(input.EmailSMTPUsername) == "" {
		return func() {}, errors.New("启用 SMTP 通知后必须填写 SMTP 用户名")
	}
	if input.EmailSMTPPassword == "" {
		return func() {}, errors.New("启用 SMTP 通知后必须填写 SMTP 密码")
	}
	if cfg.Paths.Config == "" {
		return func() {}, errors.New("SMTP 敏感凭据存储目录不能为空")
	}
	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		return func() {}, fmt.Errorf("初始化 SMTP 凭据存储失败：%w", err)
	}
	reference, err := newSMTPCredentialReference()
	if err != nil {
		return func() {}, err
	}
	if err := store.Put(ctx, reference, map[string]string{
		"username": strings.TrimSpace(input.EmailSMTPUsername),
		"password": input.EmailSMTPPassword,
	}); err != nil {
		return func() {}, fmt.Errorf("保存 SMTP 凭据失败：%w", err)
	}
	input.EmailSMTPCredentialRef = reference
	return func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = store.Delete(cleanupContext, reference)
	}, nil
}

func newSMTPCredentialReference() (string, error) {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("生成 SMTP 凭据引用失败：%w", err)
	}
	return "secret://notification/smtp/" + hex.EncodeToString(buffer), nil
}

func prepareDNSCredentials(ctx context.Context, cfg config.Config, input *setup.Input, output io.Writer) (func(), error) {
	if input == nil {
		return func() {}, errors.New("初始化输入不能为空")
	}
	if strings.TrimSpace(input.DNSCredentialRef) != "" {
		return verifyExistingDNSCredentials(ctx, cfg, input, output, dnsprovider.NewAliyunAuthorizationChecker())
	}
	if len(input.DNSCredentials) == 0 {
		return func() {}, errors.New("DNS 凭据不能为空")
	}
	if strings.TrimSpace(input.DNSTestDomain) == "" {
		return func() {}, errors.New("DNS 权限测试域名不能为空")
	}

	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		return func() {}, fmt.Errorf("初始化 DNS 凭据存储失败：%w", err)
	}
	manager := setup.NewDNSVerificationManager(
		dnsprovider.NewTester(dnsprovider.NewLegoFactory()),
		store,
	)
	return prepareDNSCredentialsWithManagerAndTimeout(ctx, input, output, manager, store, dnsprovider.PermissionVerificationTimeoutFor(input.DNSProvider))
}

func prepareDNSCredentialsWithManager(ctx context.Context, input *setup.Input, output io.Writer, manager *setup.DNSVerificationManager, store *secretstore.Store) (func(), error) {
	timeout := dnsprovider.PermissionVerificationTimeout
	if input != nil {
		timeout = dnsprovider.PermissionVerificationTimeoutFor(input.DNSProvider)
	}
	return prepareDNSCredentialsWithManagerAndTimeout(ctx, input, output, manager, store, timeout)
}

func prepareDNSCredentialsWithManagerAndTimeout(ctx context.Context, input *setup.Input, output io.Writer, manager *setup.DNSVerificationManager, store *secretstore.Store, timeout time.Duration) (func(), error) {
	if input == nil {
		return func() {}, errors.New("初始化输入不能为空")
	}
	if manager == nil || store == nil {
		return func() {}, errors.New("DNS 凭据验证服务未初始化")
	}
	if strings.TrimSpace(input.DNSCredentialRef) != "" {
		return func() {}, nil
	}
	if len(input.DNSCredentials) == 0 {
		return func() {}, errors.New("DNS 凭据不能为空")
	}
	if strings.TrimSpace(input.DNSTestDomain) == "" {
		return func() {}, errors.New("DNS 权限测试域名不能为空")
	}
	testContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := manager.Verify(testContext, setup.DNSVerificationInput{
		ProviderID:  input.DNSProvider,
		TestDomain:  input.DNSTestDomain,
		Credentials: input.DNSCredentials,
	})
	if err != nil {
		return func() {}, fmt.Errorf("DNS API 在线验证失败：%w", err)
	}
	input.DNSCredentialRef = result.CredentialRef
	input.DNSVerified = true
	input.DNSCredentials = nil
	fmt.Fprintf(output, "DNS API 在线验证通过，凭据已加密保存，有效期至 %s。\n", result.ExpiresAt.Local().Format("2006-01-02 15:04:05"))
	return func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = store.Delete(cleanupContext, result.CredentialRef)
	}, nil
}

func askDNSProvider(reader *bufio.Reader, output io.Writer) (string, error) {
	providers := dnsprovider.Descriptors()
	fmt.Fprintln(output, "请选择 DNS API 服务商：")
	for index, provider := range providers {
		status := ""
		if !provider.Available {
			status = "（暂未开放）"
		}
		fmt.Fprintf(output, "%d. %s%s\n", index+1, provider.Name, status)
	}

	for {
		value, err := ask(reader, output, "请输入编号", "")
		if err != nil {
			return "", err
		}
		index, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || index < 1 || index > len(providers) {
			fmt.Fprintln(output, "编号无效，请重新输入。")
			continue
		}
		provider := providers[index-1]
		if !provider.Available {
			fmt.Fprintf(output, "%s 当前暂未开放，请选择其他服务商。\n", provider.Name)
			continue
		}
		return provider.ID, nil
	}
}
func ask(reader *bufio.Reader, output io.Writer, label, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Fprintf(output, "%s [%s]：", label, defaultValue)
	} else {
		fmt.Fprintf(output, "%s：", label)
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = defaultValue
	}
	return line, nil
}

func askInt(reader *bufio.Reader, output io.Writer, label string, defaultValue int) (int, error) {
	value, err := ask(reader, output, label, strconv.Itoa(defaultValue))
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是数字", label)
	}
	return parsed, nil
}

func askYesNo(reader *bufio.Reader, output io.Writer, label string, defaultValue bool) (bool, error) {
	defaultText := "否"
	if defaultValue {
		defaultText = "是"
	}
	value, err := ask(reader, output, label+"（是/否）", defaultText)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(value) {
	case "是", "y", "yes":
		return true, nil
	case "否", "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%s 请输入是或否", label)
	}
}

func splitValues(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func splitInts(value string) ([]int, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	items := strings.Split(value, ",")
	result := make([]int, 0, len(items))
	for _, item := range items {
		parsed, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil {
			return nil, fmt.Errorf("%q 不是有效数字", strings.TrimSpace(item))
		}
		result = append(result, parsed)
	}
	return result, nil
}

func parseStringMap(value string) (map[string]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var result map[string]string
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "查看服务状态", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		return printJSON(cmd, commandStatus(cfg))
	}}
}

func commandStatus(cfg config.Config) map[string]any {
	return map[string]any{"service": "scert", "version": buildinfo.Version, "config_version": cfg.Version, "setup_ready": cfg.SetupReady, "https_mode": cfg.Server.HTTPSMode, "database_driver": cfg.Database.Driver, "server_port": cfg.Server.Port, "reset_url": cfg.ResetURL(), "web_enabled": cfg.Web.Enabled, "web_hostname": cfg.Web.Hostname, "web_public_ip": cfg.Web.PublicIP, "dns_verification_state": cfg.DNS.VerificationState}
}

func newServeCommand() *cobra.Command {
	var publicIP string
	var firewallMode string
	command := &cobra.Command{Use: "serve", Short: "启动网站服务", RunE: func(cmd *cobra.Command, args []string) error {
		path, err := filepath.Abs(resolveConfigPath())
		if err != nil {
			return err
		}
		reload := func() (config.Config, error) { return loadServeConfig(path, config.Default(baseDir)) }
		cfg, err := reload()
		if err != nil {
			return err
		}
		lock, err := acquireProcessLock(cfg, "Scert 服务已在运行或备份恢复任务正在执行")
		if err != nil {
			return err
		}
		defer lock.Close()
		lockedDataPath := cfg.Paths.Data
		cfg, err = reload()
		if err != nil {
			return err
		}
		if filepath.Clean(cfg.Paths.Data) != filepath.Clean(lockedDataPath) {
			return fmt.Errorf("配置的数据目录已经变更，请重新启动服务")
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		advertisedIP := publicIP
		if !cmd.Flags().Changed("bootstrap-public-ip") {
			advertisedIP = os.Getenv("SCERT_BOOTSTRAP_PUBLIC_IP")
		}
		selectedFirewallMode := bootstrapFirewallMode(cmd, firewallMode)
		return serveLifecycle(ctx, cfg, reload,
			func(ctx context.Context, initial config.Config) (bool, error) {
				return bootstrap.Run(ctx, bootstrap.ServerOptions{
					Config: initial, ConfigPath: path, PublicIP: advertisedIP,
					URLPath: bootstrapURLPath(initial), Output: cmd.OutOrStdout(), FirewallMode: selectedFirewallMode,
				})
			},
			func(ctx context.Context, ready config.Config) (bool, error) { return runApplication(ctx, ready, path) },
		)
	}}
	command.Flags().StringVar(&publicIP, "bootstrap-public-ip", "", "首次初始化地址中的公网 IP；也可设置 SCERT_BOOTSTRAP_PUBLIC_IP")
	command.Flags().StringVar(&firewallMode, "bootstrap-firewall-mode", firewall.ModeAuto, "首次初始化防火墙模式：auto 自动管理，external 由管理员预先配置；也可设置 SCERT_BOOTSTRAP_FIREWALL_MODE")
	return command
}

func newDomainCommand() *cobra.Command {
	command := &cobra.Command{Use: "domain", Short: "管理托管域名"}
	command.AddCommand(newDomainListCommand(), newDomainAddCommand(), newDomainEditCommand(), newDomainDeleteCommand(), newDomainIssueCommand(), newDomainImportCommand(), newDomainServersCommand())
	return command
}

func newDomainIssueCommand() *cobra.Command {
	var id string
	command := &cobra.Command{Use: "issue", Short: "申请或续期域名证书", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		service, err := app.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		defer service.Close()
		if service.Workflow == nil {
			return fmt.Errorf("证书申请服务未初始化")
		}
		result, err := service.Workflow.IssueWithReport(cmd.Context(), id)
		if err != nil {
			return err
		}
		return printJSON(cmd, result)
	}}
	command.Flags().StringVar(&id, "id", "", "域名 ID")
	_ = command.MarkFlagRequired("id")
	return command
}

func newDomainImportCommand() *cobra.Command {
	var id, certificatePath, privateKeyPath, issuerPath string
	command := &cobra.Command{Use: "import", Short: "导入自有证书", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		service, err := app.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		defer service.Close()
		if service.Workflow == nil {
			return fmt.Errorf("证书导入服务未初始化")
		}
		result, err := service.Workflow.Import(cmd.Context(), certificate.ImportRequest{
			DomainID: id, Certificate: certificatePath, PrivateKey: privateKeyPath, IssuerBundle: issuerPath,
		})
		if err != nil {
			return err
		}
		return printJSON(cmd, result)
	}}
	command.Flags().StringVar(&id, "id", "", "域名 ID")
	command.Flags().StringVar(&certificatePath, "certificate", "", "证书 PEM 文件路径")
	command.Flags().StringVar(&privateKeyPath, "private-key", "", "私钥 PEM 文件路径")
	command.Flags().StringVar(&issuerPath, "issuer", "", "签发者证书 PEM 文件路径，可选")
	_ = command.MarkFlagRequired("id")
	_ = command.MarkFlagRequired("certificate")
	_ = command.MarkFlagRequired("private-key")
	return command
}

func newDomainListCommand() *cobra.Command {
	return &cobra.Command{Use: "list", Short: "查看域名列表", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		service, err := app.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		defer service.Close()
		items, err := service.Repository.List(cmd.Context())
		if err != nil {
			return err
		}
		return printJSON(cmd, items)
	}}
}

func newDomainAddCommand() *cobra.Command {
	var name, certificateType, acmeEnvironment, dnsProvider string
	var sans []string
	var autoRenew bool
	command := &cobra.Command{Use: "add", Short: "添加托管域名", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		service, err := app.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		defer service.Close()
		item, err := domain.NewDomainWithOptions(name, domain.CertificateType(certificateType), sans, autoRenew, acmeEnvironment, dnsProvider)
		if err != nil {
			return err
		}
		if err := service.Repository.Create(cmd.Context(), item); err != nil {
			return err
		}
		fmt.Printf("域名已添加：%s\n", item.Name)
		return nil
	}}
	command.Flags().StringVar(&name, "name", "", "域名")
	command.Flags().StringVar(&certificateType, "type", string(domain.CertificateSingle), "证书类型：single、wildcard、san")
	command.Flags().StringSliceVar(&sans, "sans", nil, "SAN 域名列表")
	command.Flags().BoolVar(&autoRenew, "auto-renew", true, "是否启用自动续期")
	command.Flags().StringVar(&acmeEnvironment, "acme-environment", domain.ACMEEnvironmentStaging, "ACME 环境：staging、production")
	command.Flags().StringVar(&dnsProvider, "dns-provider", "", "DNS 验证服务商标识")
	_ = command.MarkFlagRequired("name")
	return command
}

func newDomainEditCommand() *cobra.Command {
	var id, name, certificateType, acmeEnvironment, dnsProvider string
	var sans []string
	var autoRenew bool
	command := &cobra.Command{Use: "edit", Short: "编辑托管域名", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		service, err := app.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		defer service.Close()
		existing, err := service.Repository.GetByID(cmd.Context(), id)
		if err != nil {
			return err
		}
		item, err := domain.NewDomainWithOptions(name, domain.CertificateType(certificateType), sans, autoRenew, acmeEnvironment, dnsProvider)
		if err != nil {
			return err
		}
		item.ID = existing.ID
		item.CreatedAt = existing.CreatedAt
		item.Status = "pending"
		item.ExpiresAt = nil
		if err := service.Repository.Update(cmd.Context(), item); err != nil {
			return err
		}
		fmt.Printf("域名已更新：%s\n", item.Name)
		return nil
	}}
	command.Flags().StringVar(&id, "id", "", "域名 ID")
	command.Flags().StringVar(&name, "name", "", "域名")
	command.Flags().StringVar(&certificateType, "type", string(domain.CertificateSingle), "证书类型：single、wildcard、san")
	command.Flags().StringSliceVar(&sans, "sans", nil, "SAN 域名列表")
	command.Flags().BoolVar(&autoRenew, "auto-renew", true, "是否启用自动续期")
	command.Flags().StringVar(&acmeEnvironment, "acme-environment", domain.ACMEEnvironmentStaging, "ACME 环境：staging、production")
	command.Flags().StringVar(&dnsProvider, "dns-provider", "", "DNS 验证服务商标识")
	_ = command.MarkFlagRequired("id")
	_ = command.MarkFlagRequired("name")
	return command
}

func newDomainDeleteCommand() *cobra.Command {
	var id string
	command := &cobra.Command{Use: "delete", Short: "删除托管域名", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		service, err := app.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		defer service.Close()
		if err := service.Repository.Delete(cmd.Context(), id); err != nil {
			return err
		}
		fmt.Printf("域名已删除：%s\n", id)
		return nil
	}}
	command.Flags().StringVar(&id, "id", "", "域名 ID")
	_ = command.MarkFlagRequired("id")
	return command
}

func printJSON(cmd *cobra.Command, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 JSON 失败：%w", err)
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return err
}

func loadConfig() (config.Config, error) { return config.LoadWithEnv(resolveConfigPath()) }

func resolveConfigPath() string {
	if configPath != "" {
		return configPath
	}
	paths := config.DefaultPaths(baseDir)
	return filepath.Join(paths.Config, "scert.json")
}
