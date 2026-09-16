package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

// 替身只模拟工具协议和数据库状态，不构成 PostgreSQL 实机恢复证据。
type postgresFixture struct {
	t             *testing.T
	state         string
	restores      int
	failDump      bool
	failDecode    bool
	failRestore   bool
	failRollback  bool
	extraObject   bool
	cancelRestore context.CancelFunc
	tools         *postgresTools
}

func newPostgresFixture(t *testing.T) *postgresFixture {
	f := &postgresFixture{t: t, state: "current"}
	f.tools = &postgresTools{dumpPath: "dump", restorePath: "restore", run: f.run}
	return f
}

func (f *postgresFixture) factory(context.Context) (*postgresTools, error) { return f.tools, nil }

func (f *postgresFixture) run(ctx context.Context, tool string, args, env []string, output io.Writer) error {
	f.t.Helper()
	if err := ctx.Err(); err != nil {
		return err
	}
	connected := slices.ContainsFunc(args, func(arg string) bool { return strings.HasPrefix(arg, "--dbname=") })
	if connected {
		if !slices.Contains(env, "PGPASSWORD=current-password") || strings.Contains(strings.Join(args, " "), "current-password") || strings.Contains(strings.Join(args, " "), "backup-password") {
			f.t.Fatal("维护操作必须固定当前凭据且不将密码放入命令行")
		}
	}
	if tool == "dump" {
		if !slices.Contains(args, "--format=custom") || !slices.Contains(args, "--no-password") || slices.Contains(args, "--no-acl") || slices.Contains(args, "--no-owner") {
			f.t.Fatal("数据库快照工具参数不完整")
		}
		_, err := io.WriteString(output, "PGDMP"+f.state)
		if f.failDump {
			return errors.New("模拟快照失败")
		}
		return err
	}
	input := args[len(args)-1]
	data, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	if slices.Contains(args, "--list") {
		_, err := io.WriteString(output, "; Archive TOC\n10; 1259 100 TABLE public settings owner\n11; 0 100 TABLE DATA public settings owner\n")
		if f.extraObject && strings.HasPrefix(string(data), "PGDMPcurrent") {
			_, err = io.WriteString(output, "12; 1259 101 TABLE public extra owner\n")
		}
		return err
	}
	if !connected {
		if !slices.Contains(args, "--file=-") || f.failDecode {
			return errors.New("模拟归档解码失败")
		}
		return nil
	}
	if slices.Contains(args, "--no-owner") || slices.Contains(args, "--no-acl") {
		f.t.Fatal("完整恢复不能静默丢弃所有者或对象授权")
	}
	for _, flag := range []string{"--clean", "--if-exists", "--single-transaction", "--exit-on-error", "--no-password"} {
		if !slices.Contains(args, flag) {
			f.t.Fatalf("恢复缺少必要参数：%s", flag)
		}
	}
	f.restores++
	if f.restores > 1 {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Minute || ctx.Err() != nil {
			f.t.Fatal("回滚上下文必须独立且有界")
		}
		if f.failRollback {
			return errors.New("模拟数据库回滚失败")
		}
	}
	f.state = strings.TrimPrefix(string(data), "PGDMP")
	if f.restores == 1 && f.cancelRestore != nil {
		f.cancelRestore()
		return context.Canceled
	}
	if f.restores == 1 && f.failRestore {
		return errors.New("模拟导入失败")
	}
	return nil
}

func preparePostgresRestoreFixture(t *testing.T, blockedCertificate bool) (config.Config, string, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Paths = config.PathsConfig{
		Install: root, Config: filepath.Join(root, "conf"), Data: filepath.Join(root, "data"),
		Certificate: filepath.Join(root, "ssl"), Log: filepath.Join(root, "log"), Backup: filepath.Join(root, "backups"),
	}
	cfg.Database = config.DatabaseConfig{
		Driver: "postgres", DSN: "postgres://db.example.test:5432/scert?sslmode=verify-full",
		CredentialRef: "secret://database/maintenance", MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetimeMins: 30,
	}
	cfg.Server.Port = 9004
	current := filepath.Join(cfg.Paths.Config, "scert.json")
	if err := cfg.Save(current); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "input")
	backup := cfg
	backup.Server.Port = 9005
	if err := backup.Save(filepath.Join(input, "scert.json")); err != nil {
		t.Fatal(err)
	}
	for directory, password := range map[string]string{cfg.Paths.Config: "current-password", input: "backup-password"} {
		store, err := secretstore.Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Put(t.Context(), cfg.Database.CredentialRef, map[string]string{"username": "owner", "password": password}); err != nil {
			t.Fatal(err)
		}
	}
	if blockedCertificate {
		if err := os.WriteFile(cfg.Paths.Certificate, []byte("preserve-blocked-target"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.MkdirAll(cfg.Paths.Certificate, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg.Paths.Certificate, "old.pem"), []byte("old-certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(input, "certificates"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{
		filepath.Join(input, "scert.pgdump"):            "PGDMPrestored",
		filepath.Join(input, "certificates", "new.pem"): "new-certificate",
	} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeBackupManifest(input); err != nil {
		t.Fatal(err)
	}
	previous := configPath
	configPath = current
	t.Cleanup(func() { configPath = previous })
	return cfg, current, input
}

func TestPostgresCompleteBackupAndFailedPublication(t *testing.T) {
	for _, failure := range []string{"", "dump", "decode"} {
		t.Run("failure="+failure, func(t *testing.T) {
			cfg, current, _ := preparePostgresRestoreFixture(t, false)
			f := newPostgresFixture(t)
			f.failDump, f.failDecode = failure == "dump", failure == "decode"
			output := filepath.Join(t.TempDir(), "backup")
			err := createBackupWithPostgresTools(t.Context(), cfg, current, output, f.factory)
			if failure != "" {
				if err == nil {
					t.Fatal("快照或解码失败时不能报告备份成功")
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatal("失败备份不能发布")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := validateBackupManifest(output); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"scert.pgdump", "scert.json", "master.key", "secrets.json", "certificates/old.pem"} {
				if !isRegularFile(filepath.Join(output, name)) {
					t.Fatalf("完整备份缺少：%s", name)
				}
			}
			data, err := os.ReadFile(filepath.Join(output, "scert.json"))
			if err != nil || bytes.Contains(data, []byte("current-password")) {
				t.Fatal("备份配置不得写入内存连接密码")
			}
		})
	}
}

func TestPostgresRestoreSuccessAndRollback(t *testing.T) {
	for _, scenario := range []string{"success", "certificate", "import", "cancel", "rollback", "inventory", "snapshot", "decode"} {
		t.Run(scenario, func(t *testing.T) {
			cfg, current, input := preparePostgresRestoreFixture(t, scenario == "certificate")
			before, _ := os.ReadFile(current)
			beforeKey, _ := os.ReadFile(filepath.Join(cfg.Paths.Config, "master.key"))
			beforeSecrets, _ := os.ReadFile(filepath.Join(cfg.Paths.Config, "secrets.json"))
			f := newPostgresFixture(t)
			f.failRestore = scenario == "import" || scenario == "rollback"
			f.failRollback, f.extraObject = scenario == "rollback", scenario == "inventory"
			f.failDump, f.failDecode = scenario == "snapshot", scenario == "decode"
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancel" {
				f.cancelRestore = cancel
			}
			command := newRestoreCommandWithPostgresTools(f.factory)
			command.SetArgs([]string{"--input", input})
			command.SetContext(ctx)
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			err := command.Execute()
			if scenario == "success" {
				if err != nil || f.state != "restored" || f.restores != 1 {
					t.Fatalf("完整恢复未成功：%v", err)
				}
				loaded, err := config.Load(current)
				if err != nil || loaded.Server.Port != 9005 || !isRegularFile(filepath.Join(cfg.Paths.Certificate, "new.pem")) {
					t.Fatal("恢复未包含配置或证书")
				}
				store, err := secretstore.Open(cfg.Paths.Config)
				if err != nil {
					t.Fatal(err)
				}
				credentials, err := store.Get(t.Context(), cfg.Database.CredentialRef)
				if err != nil || credentials["password"] != "backup-password" {
					t.Fatal("备份加密凭据未恢复")
				}
			} else {
				if err == nil || strings.Contains(output.String(), "恢复已完成") {
					t.Fatal("恢复失败不能报告成功")
				}
				if scenario == "rollback" {
					if !strings.Contains(err.Error(), "数据库自动回滚失败") {
						t.Fatal("回滚失败必须明确要求人工恢复")
					}
				} else if f.state != "current" {
					t.Fatal("失败时没有恢复原数据库状态")
				}
				after, _ := os.ReadFile(current)
				afterKey, _ := os.ReadFile(filepath.Join(cfg.Paths.Config, "master.key"))
				afterSecrets, _ := os.ReadFile(filepath.Join(cfg.Paths.Config, "secrets.json"))
				if !bytes.Equal(before, after) || !bytes.Equal(beforeKey, afterKey) || !bytes.Equal(beforeSecrets, afterSecrets) {
					t.Fatal("失败时未保留或回滚原配置与加密凭据")
				}
				if scenario == "inventory" || scenario == "snapshot" || scenario == "decode" {
					if f.restores != 0 {
						t.Fatal("预检查失败时不应执行数据库导入")
					}
				} else if f.restores != 2 {
					t.Fatal("导入后失败应执行数据库回滚")
				}
			}
			if scenario != "snapshot" && scenario != "decode" {
				matches, _ := filepath.Glob(filepath.Join(cfg.Paths.Backup, "scert-postgres-before-restore-*", "scert.pgdump"))
				if len(matches) != 1 {
					t.Fatal("恢复前快照应保留供管理员检查")
				}
			}
		})
	}
}

func TestPostgresRestoreRejectsDifferentDatabase(t *testing.T) {
	current := config.Config{Database: config.DatabaseConfig{Driver: "postgres", DSN: "postgres://db.test/current?sslmode=verify-full"}}
	backup := current
	backup.Database.DSN = "postgres://db.test/other?sslmode=verify-full"
	if err := validateRestoreCompatibility(current, backup); err == nil || !strings.Contains(err.Error(), "拒绝跨数据库恢复") {
		t.Fatal("恢复不得连接不同目标数据库")
	}
}

func TestPostgresBackupDoesNotOverwriteExistingFile(t *testing.T) {
	cfg, _, _ := preparePostgresRestoreFixture(t, false)
	connection, err := resolveMaintenanceDatabase(t.Context(), cfg.Database, cfg.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "existing.pgdump")
	if err := os.WriteFile(output, []byte("existing-backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newPostgresFixture(t)
	if err := f.tools.backup(t.Context(), connection, output); err == nil {
		t.Fatal("既有文件不能被覆盖")
	}
	content, err := os.ReadFile(output)
	if err != nil || string(content) != "existing-backup" {
		t.Fatal("拒绝覆盖后必须保留原文件")
	}
}

func TestPostgresArchiveRejectsInvalidMagicAndInventory(t *testing.T) {
	input := filepath.Join(t.TempDir(), "scert.pgdump")
	if err := os.WriteFile(input, []byte("not-a-custom-archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newPostgresFixture(t)
	if err := f.tools.validateArchive(t.Context(), input); err == nil {
		t.Fatal("非自定义格式归档必须拒绝")
	}
	for _, listing := range []string{"", "; empty", "malformed", "1; 1259 1 TABLE public a owner\n2; 1259 2 TABLE public a owner\n", strings.Repeat("x", (8<<20)+1)} {
		tools := &postgresTools{restorePath: "restore", run: func(_ context.Context, _ string, _, _ []string, output io.Writer) error {
			_, err := io.WriteString(output, listing)
			return err
		}}
		if _, err := tools.inventory(t.Context(), input); err == nil {
			t.Fatal("空、畸形、重复或超限清单必须拒绝")
		}
	}
}

func TestPostgresCanceledBackupIsNotPublished(t *testing.T) {
	cfg, current, _ := preparePostgresRestoreFixture(t, false)
	f := newPostgresFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.tools.run = func(ctx context.Context, tool string, args, env []string, output io.Writer) error {
		err := f.run(ctx, tool, args, env, output)
		if tool == "restore" && slices.Contains(args, "--file=-") {
			cancel()
		}
		return err
	}
	output := filepath.Join(t.TempDir(), "backup")
	if err := createBackupWithPostgresTools(ctx, cfg, current, output, f.factory); !errors.Is(err, context.Canceled) {
		t.Fatalf("完成快照后取消仍应停止发布：%v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("取消的备份不能发布")
	}
}

func TestPostgresMissingRestoreToolsPreserveCurrentAssets(t *testing.T) {
	cfg, current, input := preparePostgresRestoreFixture(t, false)
	before, _ := os.ReadFile(current)
	factory := func(context.Context) (*postgresTools, error) {
		return nil, errors.New("模拟官方工具未安装")
	}
	command := newRestoreCommandWithPostgresTools(factory)
	command.SetArgs([]string{"--input", input})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.Execute(); err == nil {
		t.Fatal("缺少工具不能恢复")
	}
	after, _ := os.ReadFile(current)
	if !bytes.Equal(before, after) || !isRegularFile(filepath.Join(cfg.Paths.Certificate, "old.pem")) {
		t.Fatal("缺少工具不得改变当前配置或证书")
	}
	if _, err := os.Stat(cfg.Paths.Data); !os.IsNotExist(err) {
		t.Fatal("缺少工具时不应进入锁和数据库恢复阶段")
	}
}
