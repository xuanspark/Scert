package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"git.pcux.cn/xuans/Scert/backend/internal/app"
	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
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
		newInitCommand(), newStatusCommand(), newServeCommand(), newDomainCommand(),
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
	return &cobra.Command{Use: "init", Short: "生成初始配置", RunE: func(cmd *cobra.Command, args []string) error {
		path := resolveConfigPath()
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("配置文件已存在：%s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		cfg := config.Default(baseDir)
		if err := cfg.Save(path); err != nil {
			return err
		}
		fmt.Printf("初始配置已生成：%s\n", path)
		fmt.Println("当前默认使用 ACME Staging 环境，完成 DNS 配置后再切换 Production。")
		return nil
	}}
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
	return map[string]any{"service": "scert", "version": buildinfo.Version, "config_version": cfg.Version, "setup_ready": cfg.SetupReady, "https_mode": cfg.Server.HTTPSMode, "database_driver": cfg.Database.Driver}
}

func newServeCommand() *cobra.Command {
	return &cobra.Command{Use: "serve", Short: "启动 Web 服务", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		service, err := app.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		defer service.Close()
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
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
	command.AddCommand(newDomainListCommand(), newDomainAddCommand())
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
	var name, certificateType string
	var sans []string
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
		item, err := domain.NewDomain(name, domain.CertificateType(certificateType), sans)
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
	_ = command.MarkFlagRequired("name")
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

func loadConfig() (config.Config, error) {
	path := resolveConfigPath()
	return config.Load(path)
}

func resolveConfigPath() string {
	if configPath != "" {
		return configPath
	}
	paths := config.DefaultPaths(baseDir)
	return filepath.Join(paths.Config, "scert.json")
}
