package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"git.pcux.cn/xuans/Scert/backend/internal/app"
	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
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
	root := &cobra.Command{Use: "scert", Short: "SSL 证书管理服务", SilenceUsage: true}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(newHelpCommand())
	root.SetHelpTemplate(helpTemplate)
	root.PersistentFlags().StringVar(&configPath, "config", "", "配置文件路径")
	root.PersistentFlags().StringVar(&baseDir, "base-dir", "", "Windows 默认安装根目录")
	root.AddCommand(
		&cobra.Command{Use: "version", Short: "显示版本信息", Run: func(cmd *cobra.Command, args []string) { cmd.Printf("Scert %s\n", version) }},
		newInitCommand(), newStatusCommand(), newServeCommand(), newDomainCommand(), newServerCommand(), newSSHKeyCommand(),
	)
	localizeHelpFlags(root)
	return root
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
	var (
		adminUsername, adminEmail, adminPassword, emailProvider, emailEndpoint string
		recipients, dnsProvider, dnsCredentialRef, certificateRoot             string
		webHostname, webPublicIP, httpsMode                                    string
		serverPort                                                             int
		dnsVerified, webEnabled                                                bool
	)
	command := &cobra.Command{Use: "init", Short: "交互式初始化配置", RunE: func(cmd *cobra.Command, args []string) error {
		path := resolveConfigPath()
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("配置文件已存在：%s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		cfg := config.Default(baseDir)
		input := setup.Input{
			AdminUsername: adminUsername, AdminEmail: adminEmail, AdminPassword: adminPassword, ServerPort: serverPort,
			EmailProvider: emailProvider, EmailEndpoint: emailEndpoint,
			EmailRecipients: splitValues(recipients), DNSProvider: dnsProvider, DNSCredentialRef: dnsCredentialRef,
			DNSVerified: dnsVerified, CertificateRoot: certificateRoot, WebHostname: webHostname,
			WebPublicIP: webPublicIP, WebEnabled: webEnabled, HTTPSMode: httpsMode,
		}
		if !cmd.Flags().Changed("admin-username") {
			if err := readInteractiveInit(cmd.InOrStdin(), cmd.OutOrStdout(), &input, cfg); err != nil {
				return err
			}
		}
		if err := setup.Apply(&cfg, input); err != nil {
			return err
		}
		firewallResult, err := saveSetupWithFirewall(cmd.Context(), cfg, path, firewallManager)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "初始化配置已保存：%s\n", path)
		fmt.Fprintf(cmd.OutOrStdout(), "DNS API 已标记为验证通过；真实 provider 连通性测试和 Web 证书申请将在后续任务接入。\n")
		fmt.Fprintf(cmd.OutOrStdout(), "本机防火墙：%s\n", firewallResult.Message)
		fmt.Fprintf(cmd.OutOrStdout(), "请确认云安全组已放行主服务端口 %d。Scert-admin 默认关闭；需要重置密码时启动后访问：%s\n", cfg.Server.Port, cfg.ResetURL())
		return nil
	}}
	command.Flags().StringVar(&adminUsername, "admin-username", "", "超级管理员用户名")
	command.Flags().StringVar(&adminEmail, "admin-email", "", "超级管理员绑定邮箱")
	command.Flags().StringVar(&adminPassword, "admin-password", "", "超级管理员密码（仅建议临时使用参数，优先交互输入）")
	command.Flags().IntVar(&serverPort, "server-port", 8080, "Scert 主服务端口")
	command.Flags().StringVar(&emailProvider, "email-provider", "", "第三方邮箱服务标识")
	command.Flags().StringVar(&emailEndpoint, "email-endpoint", "", "第三方邮箱 API 地址")
	command.Flags().StringVar(&recipients, "recipients", "", "通知收件人，多个地址用逗号分隔")
	command.Flags().StringVar(&dnsProvider, "dns-provider", "", "DNS API 服务商标识")
	command.Flags().StringVar(&dnsCredentialRef, "dns-credential-ref", "", "DNS 凭据引用，不保存明文密钥")
	command.Flags().BoolVar(&dnsVerified, "dns-verified", false, "DNS API 是否已通过外部验证测试")
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
	port, err := askInt(reader, output, "Scert 主服务端口 *", cfg.Server.Port)
	if err != nil {
		return err
	}
	value.ServerPort = port
	value.EmailProvider, err = ask(reader, output, "第三方邮箱服务标识", "")
	if err != nil {
		return err
	}
	value.EmailEndpoint, err = ask(reader, output, "第三方邮箱 API 地址", "")
	if err != nil {
		return err
	}
	receiver, err := ask(reader, output, "通知收件人（逗号分隔）", "")
	if err != nil {
		return err
	}
	value.EmailRecipients = splitValues(receiver)
	value.DNSProvider, err = ask(reader, output, "DNS API 服务商 *", "")
	if err != nil {
		return err
	}
	value.DNSCredentialRef, err = ask(reader, output, "DNS 凭据引用 *（不要输入明文密钥）", "")
	if err != nil {
		return err
	}
	value.DNSVerified, err = askYesNo(reader, output, "DNS API 是否已经完成验证测试？", false)
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
	return &cobra.Command{Use: "serve", Short: "启动网站服务", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if !cfg.SetupReady {
			cfg.Server.Host = "127.0.0.1"
			fmt.Fprintln(cmd.OutOrStdout(), "系统尚未初始化，临时服务仅监听本机；请完成初始化后重启服务。")
		}
		if cfg.SetupReady && !cfg.Web.Enabled {
			return fmt.Errorf("管理页面已关闭")
		}
		service, err := app.NewWithConfigPath(cmd.Context(), cfg, resolveConfigPath())
		if err != nil {
			return err
		}
		defer service.Close()
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if service.Renewal != nil && cfg.SetupReady {
			service.Renewal.Start(ctx)
		}
		errCh := make(chan error, 1)
		go func() { errCh <- service.HTTP.ListenAndServe() }()
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	}}
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

func loadConfig() (config.Config, error) { return config.Load(resolveConfigPath()) }

func resolveConfigPath() string {
	if configPath != "" {
		return configPath
	}
	paths := config.DefaultPaths(baseDir)
	return filepath.Join(paths.Config, "scert.json")
}
