package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"git.pcux.cn/xuans/Scert/backend/internal/app"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/processlock"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

func newCertificateCommand() *cobra.Command {
	command := &cobra.Command{Use: "certificate", Short: "查询和校验证书"}
	command.AddCommand(newCertificateListCommand(), newCertificateShowCommand(), newCertificateValidateCommand())
	return command
}

func newCertificateListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "查看证书列表",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, err := openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer service.Close()
			items, err := service.Repository.List(cmd.Context())
			if err != nil {
				return err
			}
			result := make([]map[string]any, 0, len(items))
			for _, item := range items {
				entry := map[string]any{"domain": item}
				current, currentErr := service.Certificates.Current(cmd.Context(), item.ID)
				if currentErr != nil {
					entry["certificate_error"] = currentErr.Error()
				} else {
					entry["certificate"] = current
				}
				result = append(result, entry)
			}
			return printJSON(cmd, result)
		},
	}
}

func newCertificateShowCommand() *cobra.Command {
	var id string
	command := &cobra.Command{
		Use:   "show",
		Short: "查看当前证书详情",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(id) == "" {
				return errors.New("域名 ID 不能为空")
			}
			service, err := openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer service.Close()
			result, err := service.Certificates.Current(cmd.Context(), id)
			if err != nil {
				return err
			}
			return printJSON(cmd, result)
		},
	}
	command.Flags().StringVar(&id, "id", "", "域名 ID")
	_ = command.MarkFlagRequired("id")
	return command
}

func newCertificateValidateCommand() *cobra.Command {
	var id string
	command := &cobra.Command{
		Use:   "validate",
		Short: "校验当前证书",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(id) == "" {
				return errors.New("域名 ID 不能为空")
			}
			service, err := openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer service.Close()
			result, err := service.Certificates.ValidateCurrent(cmd.Context(), id)
			if err != nil {
				return err
			}
			return printJSON(cmd, map[string]any{"valid": true, "certificate": result})
		},
	}
	command.Flags().StringVar(&id, "id", "", "域名 ID")
	_ = command.MarkFlagRequired("id")
	return command
}

func newRenewCommand() *cobra.Command {
	var id string
	command := &cobra.Command{
		Use:   "renew",
		Short: "执行自动续期检查或续期指定域名",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, err := openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer service.Close()
			if strings.TrimSpace(id) != "" {
				result, err := service.Workflow.IssueWithReport(cmd.Context(), id)
				if err != nil {
					return err
				}
				return printJSON(cmd, result)
			}
			if service.Renewal == nil {
				return errors.New("自动续期服务未初始化")
			}
			result, err := service.Renewal.RunOnce(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(cmd, result)
		},
	}
	command.Flags().StringVar(&id, "id", "", "域名 ID；留空时检查所有需要续期的域名")
	return command
}

func newDeployCommand() *cobra.Command {
	var id string
	command := &cobra.Command{
		Use:   "deploy",
		Short: "部署指定域名的当前证书",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(id) == "" {
				return errors.New("域名 ID 不能为空")
			}
			service, err := openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer service.Close()
			result, err := service.Workflow.DeployCurrent(cmd.Context(), id)
			if err != nil {
				return err
			}
			return printJSON(cmd, result)
		},
	}
	command.Flags().StringVar(&id, "id", "", "域名 ID")
	_ = command.MarkFlagRequired("id")
	return command
}

func newLogsCommand() *cobra.Command {
	var lines int
	command := &cobra.Command{
		Use:   "logs",
		Short: "查看服务日志",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if lines < 1 || lines > 1000 {
				return errors.New("日志行数必须在 1 到 1000 之间")
			}
			path := filepath.Join(cfg.Paths.Log, "scert.log")
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("打开日志文件失败：%w", err)
			}
			defer file.Close()
			values, err := tailLines(file, lines)
			if err != nil {
				return err
			}
			for _, value := range values {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), value); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().IntVar(&lines, "lines", 100, "显示最后多少行日志")
	return command
}

func newConfigCommand() *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "查看和修改服务配置"}
	command.AddCommand(newConfigShowCommand(), newConfigSetCommand(), newConfigSetSecretCommand(), newConfigValidateCommand())
	return command
}

func newConfigShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "查看脱敏后的配置",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			return printJSON(cmd, safeConfigMap(cfg))
		},
	}
}

func newConfigSetSecretCommand() *cobra.Command {
	var key, value string
	command := &cobra.Command{
		Use:   "set-secret",
		Short: "安全更新 SMTP 敏感凭据",
		RunE: func(cmd *cobra.Command, args []string) error {
			if key != "notification.smtp_password" {
				return fmt.Errorf("不支持更新的敏感配置项：%s", key)
			}
			if value == "" {
				var err error
				value, err = ask(bufio.NewReader(cmd.InOrStdin()), cmd.OutOrStdout(), "请输入新的 SMTP 密码或授权码", "")
				if err != nil {
					return err
				}
			}
			if value == "" {
				return errors.New("SMTP 密码不能为空")
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			username := strings.TrimSpace(cfg.Notification.SMTPUsername)
			if username == "" {
				return errors.New("填写 SMTP 密码前必须配置 SMTP 用户名")
			}
			store, err := secretstore.Open(cfg.Paths.Config)
			if err != nil {
				return fmt.Errorf("初始化 SMTP 凭据存储失败：%w", err)
			}
			ref, err := newSMTPCredentialReference()
			if err != nil {
				return err
			}
			if err := store.Put(cmd.Context(), ref, map[string]string{"username": username, "password": value}); err != nil {
				return fmt.Errorf("保存 SMTP 凭据失败：%w", err)
			}
			cfg.Notification.SMTPCredentialRef = ref
			if err := cfg.Save(resolveConfigPath()); err != nil {
				cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				_ = store.Delete(cleanupContext, ref)
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "SMTP 凭据已加密更新。")
			return err
		},
	}
	command.Flags().StringVar(&key, "key", "", "敏感配置项名称")
	command.Flags().StringVar(&value, "value", "", "SMTP 密码；留空时交互输入")
	_ = command.MarkFlagRequired("key")
	return command
}
func newConfigValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "校验服务配置",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "配置校验通过")
			return err
		},
	}
}

func newConfigSetCommand() *cobra.Command {
	var key, value string
	command := &cobra.Command{
		Use:   "set",
		Short: "修改允许调整的配置项",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if err := setConfigValue(&cfg, key, value); err != nil {
				return err
			}
			if err := cfg.Save(resolveConfigPath()); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "配置已更新：%s\n", key)
			return err
		},
	}
	command.Flags().StringVar(&key, "key", "", "配置项名称")
	command.Flags().StringVar(&value, "value", "", "配置项值")
	_ = command.MarkFlagRequired("key")
	_ = command.MarkFlagRequired("value")
	return command
}

func newBackupCommand() *cobra.Command {
	var output string
	command := &cobra.Command{
		Use:   "backup",
		Short: "备份配置和数据库",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			lock, err := acquireProcessLock(cfg, "服务正在运行或另一个备份恢复任务正在执行；请先停止 Scert 服务")
			if err != nil {
				return err
			}
			defer lock.Close()
			if strings.TrimSpace(output) == "" {
				output = filepath.Join(cfg.Paths.Backup, "scert-"+time.Now().UTC().Format("20060102-150405.000000000"))
			}
			output = filepath.Clean(output)
			if err := createBackup(cmd.Context(), cfg, resolveConfigPath(), output); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "备份已创建：%s\n", output)
			return err
		},
	}
	command.Flags().StringVar(&output, "output", "", "备份目录")
	return command
}

func newRestoreCommand() *cobra.Command {
	var input string
	command := &cobra.Command{
		Use:   "restore",
		Short: "恢复配置和数据库备份",
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := validateRestoreInput(input)
			if err != nil {
				return err
			}
			if err := validateBackupManifest(input); err != nil {
				return err
			}
			configFile := filepath.Join(input, "scert.json")
			cfg, err := config.Load(configFile)
			if err != nil {
				return fmt.Errorf("读取备份配置失败：%w", err)
			}
			if err := validateBackupSecretStore(cmd.Context(), input, cfg); err != nil {
				return err
			}

			currentPath := filepath.Clean(resolveConfigPath())
			expectedConfigPath := filepath.Join(cfg.Paths.Config, "scert.json")
			if !samePath(currentPath, expectedConfigPath) {
				return fmt.Errorf("恢复目标配置路径必须与备份配置目录一致：%s", expectedConfigPath)
			}
			currentConfig, currentConfigErr := config.Load(currentPath)
			if currentConfigErr != nil && !errors.Is(currentConfigErr, os.ErrNotExist) {
				return fmt.Errorf("读取当前配置失败：%w", currentConfigErr)
			}
			if currentConfigErr == nil {
				if err := validateRestoreCompatibility(currentConfig, cfg); err != nil {
					return err
				}
			}
			lockConfig := cfg
			if currentConfigErr == nil {
				lockConfig = currentConfig
			}
			lock, err := acquireProcessLock(lockConfig, "服务正在运行或另一个备份恢复任务正在执行；请先停止 Scert 服务")
			if err != nil {
				return err
			}
			defer lock.Close()
			if info, statErr := os.Lstat(currentPath); statErr == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
				return errors.New("当前配置路径不是普通文件")
			} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("检查当前配置失败：%w", statErr)
			}

			mysqlSource := filepath.Join(input, "scert.sql")
			if cfg.Database.Driver == "mysql" && !isRegularFile(mysqlSource) {
				return errors.New("MySQL 备份目录缺少有效的 scert.sql 文件")
			}

			databaseSource := filepath.Join(input, "scert.db")
			databaseSourceInfo, databaseSourceErr := os.Lstat(databaseSource)
			databaseSourceExists := databaseSourceErr == nil && databaseSourceInfo.Mode().IsRegular()
			if databaseSourceErr != nil && !errors.Is(databaseSourceErr, os.ErrNotExist) {
				return fmt.Errorf("检查备份数据库失败：%w", databaseSourceErr)
			}
			if databaseSourceErr == nil && !databaseSourceInfo.Mode().IsRegular() {
				return errors.New("备份数据库不是普通文件")
			}
			databaseTarget := ""
			sqliteMemory := false
			if cfg.Database.Driver == "sqlite" {
				databaseTarget, sqliteMemory, err = sqliteDatabasePath(cfg.Database.DSN)
				if err != nil {
					return err
				}
			}
			if cfg.Database.Driver == "sqlite" && !sqliteMemory && !databaseSourceExists {
				return errors.New("SQLite 备份目录缺少有效的 scert.db 文件")
			}
			if cfg.Database.Driver == "sqlite" && !sqliteMemory && databaseSourceExists {
				if err := validateSQLiteDatabase(cmd.Context(), databaseSource); err != nil {
					return err
				}
				if samePath(databaseSource, databaseTarget) {
					return errors.New("备份数据库与当前数据库路径相同")
				}
				if err := ensureParentDirectory(databaseTarget); err != nil {
					return fmt.Errorf("创建当前数据库父目录失败：%w", err)
				}
				if databaseTargetInfo, statErr := os.Lstat(databaseTarget); statErr == nil && (databaseTargetInfo.Mode()&os.ModeSymlink != 0 || !databaseTargetInfo.Mode().IsRegular()) {
					return errors.New("当前数据库不是普通文件")
				} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
					return fmt.Errorf("检查当前数据库失败：%w", statErr)
				}
			}

			configBackupPath := ""
			if isRegularFile(currentPath) {
				configBackupPath = currentPath + ".before-restore-" + time.Now().UTC().Format("20060102-150405.000000000")
				if err := copyFileExclusive(currentPath, configBackupPath, 0o600); err != nil {
					return err
				}
			}

			databaseBackupPath := ""
			if cfg.Database.Driver == "sqlite" && !sqliteMemory && databaseSourceExists && isRegularFile(databaseTarget) {
				databaseBackupPath = databaseTarget + ".before-restore-" + time.Now().UTC().Format("20060102-150405.000000000")
				if err := copyFileExclusive(databaseTarget, databaseBackupPath, 0o600); err != nil {
					return err
				}
			}

			mysqlBackupPath := ""
			if cfg.Database.Driver == "mysql" {
				mysqlBackupPath, err = createMySQLRestoreBackup(cmd.Context(), cfg)
				if err != nil {
					return err
				}
			}

			configRestoreAttempted := false
			sqliteRestoreAttempted := false
			mysqlRestoreAttempted := false
			rollbackCore := func() error {
				var rollbackErr error
				if mysqlRestoreAttempted && mysqlBackupPath != "" {
					rollbackErr = errors.Join(rollbackErr, restoreMySQL(cmd.Context(), cfg, mysqlBackupPath))
				}
				if sqliteRestoreAttempted {
					if databaseBackupPath != "" {
						rollbackErr = errors.Join(rollbackErr, copyFileAtomic(databaseBackupPath, databaseTarget, 0o600))
					} else {
						rollbackErr = errors.Join(rollbackErr, removeIfExists(databaseTarget))
					}
				}
				if configRestoreAttempted {
					if configBackupPath != "" {
						rollbackErr = errors.Join(rollbackErr, copyFileAtomic(configBackupPath, currentPath, 0o600))
					} else {
						rollbackErr = errors.Join(rollbackErr, removeIfExists(currentPath))
					}
				}
				return rollbackErr
			}

			configRestoreAttempted = true
			if err := copyFileAtomic(configFile, currentPath, 0o600); err != nil {
				return errors.Join(err, rollbackCore())
			}
			if cfg.Database.Driver == "sqlite" && !sqliteMemory && databaseSourceExists {
				sqliteRestoreAttempted = true
				if err := copyFileAtomic(databaseSource, databaseTarget, 0o600); err != nil {
					return errors.Join(fmt.Errorf("恢复 SQLite 数据库失败：%w", err), rollbackCore())
				}
			}
			if cfg.Database.Driver == "mysql" {
				mysqlRestoreAttempted = true
				if err := restoreMySQL(cmd.Context(), cfg, mysqlSource); err != nil {
					return errors.Join(fmt.Errorf("恢复 MySQL 数据库失败：%w", err), rollbackCore())
				}
			}
			if err := restoreAdditionalAssets(cfg, input); err != nil {
				return errors.Join(fmt.Errorf("恢复敏感配置或证书失败：%w", err), rollbackCore())
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "恢复已完成：%s\n", input)
			return err
		},
	}
	command.Flags().StringVar(&input, "input", "", "备份目录")
	_ = command.MarkFlagRequired("input")
	return command
}

func openApp(ctx context.Context) (*app.App, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return app.NewWithConfigPath(ctx, cfg, resolveConfigPath())
}

func acquireProcessLock(cfg config.Config, busyMessage string) (*processlock.Lock, error) {
	lock, err := processlock.Acquire(filepath.Join(cfg.Paths.Data, "scert.lock"))
	if errors.Is(err, processlock.ErrLocked) {
		return nil, errors.New(busyMessage)
	}
	if err != nil {
		return nil, err
	}
	return lock, nil
}

func tailLines(reader io.Reader, count int) ([]string, error) {
	values := make([]string, 0, count)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		if len(values) == count {
			copy(values, values[1:])
			values[len(values)-1] = scanner.Text()
			continue
		}
		values = append(values, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取日志失败：%w", err)
	}
	return values, nil
}

func safeConfigMap(cfg config.Config) map[string]any {
	data, _ := json.Marshal(cfg)
	result := make(map[string]any)
	_ = json.Unmarshal(data, &result)
	if admin, ok := result["admin"].(map[string]any); ok {
		if _, exists := admin["password_hash"]; exists {
			admin["password_hash"] = "已配置"
		}
	}
	return result
}

func setConfigValue(cfg *config.Config, key, value string) error {
	if cfg == nil {
		return errors.New("配置不能为空")
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("配置值不能为空")
	}
	parseBool := func() (bool, error) { return strconv.ParseBool(value) }
	parseInt := func() (int, error) { return strconv.Atoi(value) }
	switch key {
	case "server.host":
		cfg.Server.Host = value
	case "server.port":
		parsed, err := parseInt()
		if err != nil {
			return fmt.Errorf("端口格式无效：%w", err)
		}
		cfg.Server.Port = parsed
	case "server.https_mode":
		cfg.Server.HTTPSMode = value
	case "web.enabled":
		parsed, err := parseBool()
		if err != nil {
			return fmt.Errorf("管理页面开关格式无效：%w", err)
		}
		cfg.Web.Enabled = parsed
	case "web.hostname":
		cfg.Web.Hostname = value
	case "web.public_ip":
		cfg.Web.PublicIP = value
	case "paths.install":
		cfg.Paths.Install = value
	case "paths.certificate":
		cfg.Paths.Certificate = value
	case "paths.config":
		cfg.Paths.Config = value
	case "paths.data":
		cfg.Paths.Data = value
	case "paths.log":
		cfg.Paths.Log = value
	case "paths.backup":
		cfg.Paths.Backup = value
	case "acme.environment":
		cfg.ACME.Environment = value
	case "acme.account_email":
		cfg.ACME.AccountEmail = value
	case "acme.renewal_threshold_days":
		parsed, err := parseInt()
		if err != nil {
			return fmt.Errorf("续期阈值格式无效：%w", err)
		}
		cfg.ACME.RenewalThresholdDays = parsed
	case "acme.propagation_timeout_secs":
		parsed, err := parseInt()
		if err != nil {
			return fmt.Errorf("DNS 传播等待时间格式无效：%w", err)
		}
		cfg.ACME.PropagationTimeoutSecs = parsed
	case "acme.propagation_polling_interval_secs":
		parsed, err := parseInt()
		if err != nil {
			return fmt.Errorf("DNS 传播检测间隔格式无效：%w", err)
		}
		cfg.ACME.PropagationPollingIntervalSecs = parsed
	case "dns.provider":
		cfg.DNS.Provider = value
	case "dns.credential_ref":
		cfg.DNS.CredentialRef = value
	case "dns.verification_state":
		cfg.DNS.VerificationState = value
	case "notification.http_enabled":
		parsed, err := parseBool()
		if err != nil {
			return fmt.Errorf("HTTP 通知开关格式无效：%w", err)
		}
		cfg.Notification.HTTPEnabled = parsed
	case "notification.smtp_enabled":
		parsed, err := parseBool()
		if err != nil {
			return fmt.Errorf("SMTP 通知开关格式无效：%w", err)
		}
		cfg.Notification.SMTPEnabled = parsed

	case "notification.smtp_host":
		cfg.Notification.SMTPHost = value
	case "notification.smtp_port":
		parsed, err := parseInt()
		if err != nil {
			return fmt.Errorf("SMTP 端口格式无效：%w", err)
		}
		cfg.Notification.SMTPPort = parsed
	case "notification.smtp_username":
		cfg.Notification.SMTPUsername = value
	case "notification.smtp_mode":
		cfg.Notification.SMTPMode = value
	case "notification.smtp_allow_plaintext_auth":
		parsed, err := parseBool()
		if err != nil {
			return fmt.Errorf("SMTP 明文认证开关格式无效：%w", err)
		}
		cfg.Notification.SMTPAllowPlaintextAuth = parsed
	case "notification.smtp_password":
		return errors.New("SMTP 密码属于敏感信息，请使用 scert config set-secret --key notification.smtp_password")
	case "notification.provider":
		cfg.Notification.Provider = value
	case "notification.endpoint":
		cfg.Notification.Endpoint = value
	case "notification.http_path":
		cfg.Notification.HTTPPath = value
	case "notification.http_login_path":
		cfg.Notification.HTTPLoginPath = value
	case "notification.http_auth_mode":
		cfg.Notification.HTTPAuthMode = value
	case "notification.http_credential_ref":
		cfg.Notification.HTTPCredentialRef = value
	case "notification.event_mode":
		cfg.Notification.EventMode = value
	case "notification.sender_name":
		cfg.Notification.SenderName = value
	case "notification.sender_email":
		cfg.Notification.SenderEmail = value
	case "notification.recipients":
		cfg.Notification.Recipients = splitCommaValues(value)
	case "notification.timeout_secs":
		parsed, err := parseInt()
		if err != nil {
			return fmt.Errorf("通知超时时间格式无效：%w", err)
		}
		cfg.Notification.TimeoutSecs = parsed
	default:
		return fmt.Errorf("不允许通过命令行修改配置项：%s", key)
	}
	return cfg.Validate()
}

func splitCommaValues(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func copyFile(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("打开文件失败：%w", err)
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("创建目标目录失败：%w", err)
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("创建目标文件失败：%w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("复制文件失败：%w", err)
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return fmt.Errorf("同步目标文件失败：%w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("关闭目标文件失败：%w", err)
	}
	return os.Chmod(target, mode)
}

func copyFileAtomic(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("打开文件失败：%w", err)
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("创建目标目录失败：%w", err)
	}
	output, err := os.CreateTemp(filepath.Dir(target), ".scert-restore-")
	if err != nil {
		return fmt.Errorf("创建恢复暂存文件失败：%w", err)
	}
	temporaryPath := output.Name()
	completed := false
	defer func() {
		if !completed {
			_ = output.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := output.Chmod(mode); err != nil {
		return fmt.Errorf("设置恢复暂存文件权限失败：%w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("复制文件失败：%w", err)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("同步恢复暂存文件失败：%w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("关闭恢复暂存文件失败：%w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("原子替换恢复文件失败：%w", err)
	}
	if err := syncDirectory(filepath.Dir(target)); err != nil {
		return fmt.Errorf("同步恢复目标目录失败：%w", err)
	}
	completed = true
	return nil
}

func copyFileExclusive(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("打开文件失败：%w", err)
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("创建目标目录失败：%w", err)
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("创建目标文件失败：%w", err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = output.Close()
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("复制文件失败：%w", err)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("同步目标文件失败：%w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("关闭目标文件失败：%w", err)
	}
	if err := os.Chmod(target, mode); err != nil {
		return err
	}
	completed = true
	return nil
}

func ensureBackupDestination(path string) error {
	if strings.TrimSpace(path) == "" || path == "." {
		return errors.New("备份目录不能为空")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("备份目录已存在，为避免覆盖原备份请更换路径：%s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查备份目录失败：%w", err)
	}
	return nil
}

func validateRestoreInput(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return "", errors.New("备份目录不能为空")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("检查备份目录失败：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("备份路径必须是普通目录，不能使用符号链接")
	}
	configFile := filepath.Join(path, "scert.json")
	if !isRegularFile(configFile) {
		return "", errors.New("备份目录缺少有效的 scert.json 文件")
	}
	return path, nil
}

func isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func ensureParentDirectory(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("目标目录不可用：%w", err)
	}
	if !info.IsDir() {
		return errors.New("目标父路径不是目录")
	}
	return nil
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(filepath.Clean(left))
	rightAbs, rightErr := filepath.Abs(filepath.Clean(right))
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.EqualFold(leftAbs, rightAbs)
}
