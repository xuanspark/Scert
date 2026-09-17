package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

func TestPostgresRealBackupRestore(t *testing.T) {
	f := startRealPostgres(t)
	for _, scenario := range []string{"success", "certificate", "cancel", "permission", "inventory", "authentication", "tls"} {
		t.Run(scenario, func(t *testing.T) {
			dbName := "scert_" + scenario
			realPostgresExec(t, f.admin, "CREATE DATABASE "+dbName+" OWNER scert_owner")
			conn := f.connect(t, dbName, "scert_owner")
			var tls bool
			if err := conn.QueryRow(t.Context(), "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()").Scan(&tls); err != nil || !tls {
				t.Fatal("真实数据库连接必须启用 TLS")
			}
			root := filepath.Join(f.root, scenario)
			cfg := config.Default(root)
			cfg.Paths = config.PathsConfig{
				Install: root, Config: filepath.Join(root, "conf"), Data: filepath.Join(root, "data"),
				Certificate: filepath.Join(root, "ssl"), Log: filepath.Join(root, "log"), Backup: filepath.Join(root, "snapshots"),
			}
			cfg.Database = config.DatabaseConfig{Driver: "postgres", DSN: f.dsn(dbName, ""), CredentialRef: "secret://database/maintenance", MaxOpenConns: 4, MaxIdleConns: 2}
			current := filepath.Join(cfg.Paths.Config, "scert.json")
			if err := cfg.Save(current); err != nil {
				t.Fatal(err)
			}
			store, err := secretstore.Open(cfg.Paths.Config)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Put(t.Context(), cfg.Database.CredentialRef, map[string]string{"username": "scert_owner", "password": f.password}); err != nil {
				t.Fatal(err)
			}
			connection, err := resolveMaintenanceDatabase(t.Context(), cfg.Database, cfg.Paths.Config)
			if err != nil {
				t.Fatal(err)
			}
			db, err := database.Open(t.Context(), connection)
			if err != nil {
				t.Fatal("真实数据库迁移连接失败")
			}
			err = database.Migrate(t.Context(), db.DB, "postgres")
			_ = db.Close()
			if err != nil {
				t.Fatal(err)
			}
			realPostgresExec(t, conn, "INSERT INTO settings (key, value) VALUES ('integration', 'backup')")
			realPostgresExec(t, conn, "INSERT INTO audit_logs (action, resource_type, resource_id, status, message) VALUES ('backup', 'test', '1', 'ok', 'backup')")
			realPostgresExec(t, conn, "GRANT SELECT ON settings TO scert_reader")
			if scenario == "permission" {
				admin := f.connect(t, dbName, "postgres")
				realPostgresExec(t, admin, "CREATE TABLE protected_record (value TEXT)")
				realPostgresExec(t, admin, "INSERT INTO protected_record VALUES ('preserved')")
				realPostgresExec(t, admin, "GRANT SELECT ON protected_record TO scert_owner")
			}
			if err := os.MkdirAll(cfg.Paths.Certificate, 0o700); err != nil {
				t.Fatal(err)
			}
			certificate := filepath.Join(cfg.Paths.Certificate, "integration.pem")
			certData, err := os.ReadFile(f.ca)
			if err != nil {
				t.Fatal(err)
			}
			realPostgresWrite(t, certificate, certData)
			previous := configPath
			configPath = current
			t.Cleanup(func() { configPath = previous })
			input := filepath.Join(root, "backup")
			if _, err := f.runCLI(t, current, "backup", "--output", input); err != nil {
				t.Fatal(err)
			}
			if err := validateBackupManifest(input); err != nil {
				t.Fatal(err)
			}
			if scenario == "authentication" || scenario == "tls" {
				if scenario == "authentication" {
					if err := store.Put(t.Context(), cfg.Database.CredentialRef, map[string]string{"username": "scert_owner", "password": "incorrect-password"}); err != nil {
						t.Fatal(err)
					}
				} else {
					// 使用不信任服务端的独立 CA，验证不能静默降级 TLS。
					wrongCA := filepath.Join(root, "untrusted.crt")
					realPostgresTLS(t, wrongCA, filepath.Join(root, "untrusted.key"))
					cfg.Database.DSN = postgresIntegrationCA(t, cfg.Database.DSN, wrongCA)
					if err := cfg.Save(current); err != nil {
						t.Fatal(err)
					}
				}
				output := filepath.Join(root, "rejected-backup")
				if _, err := f.runCLI(t, current, "backup", "--output", output); err == nil || strings.Contains(err.Error(), f.password) {
					t.Fatal("认证或 TLS 错误必须拒绝且不得泄漏密码")
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatal("认证或 TLS 失败不得发布备份")
				}
				return
			}
			realPostgresExec(t, conn, "UPDATE settings SET value = 'current' WHERE key = 'integration'")
			realPostgresExec(t, conn, "INSERT INTO audit_logs (action, resource_type, resource_id, status, message) VALUES ('current', 'test', '2', 'ok', 'current')")
			cfg.Server.Port++
			if err := cfg.Save(current); err != nil {
				t.Fatal(err)
			}
			if err := store.Put(t.Context(), "secret://integration/marker", map[string]string{"value": "current"}); err != nil {
				t.Fatal(err)
			}
			realPostgresWrite(t, certificate, []byte("current-certificate"))
			if scenario == "certificate" {
				if err := os.Rename(cfg.Paths.Certificate, cfg.Paths.Certificate+"-preserved"); err != nil {
					t.Fatal(err)
				}
				realPostgresWrite(t, cfg.Paths.Certificate, []byte("blocked-target"))
			}
			if scenario == "inventory" {
				realPostgresExec(t, conn, "CREATE TABLE extra_record (value TEXT)")
			}
			before := make(map[string][]byte)
			for _, name := range []string{"scert.json", "master.key", "secrets.json"} {
				before[name], err = os.ReadFile(filepath.Join(cfg.Paths.Config, name))
				if err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			command := newRestoreCommand()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancel" {
				// 真正导入完成后确定性取消，反向回滚仍执行官方工具和真实数据库。
				command = newRestoreCommandWithPostgresTools(func(ctx context.Context) (*postgresTools, error) {
					tools, err := loadPostgresTools(ctx)
					if err != nil {
						return nil, err
					}
					tools.run = func(ctx context.Context, path string, args, env []string, output io.Writer) error {
						err := executePostgresTool(ctx, path, args, env, output)
						if err == nil && slices.Contains(args, "--single-transaction") && args[len(args)-1] == filepath.Join(input, "scert.pgdump") {
							cancel()
						}
						return err
					}
					return tools, nil
				})
			}
			command.SetArgs([]string{"--input", input})
			command.SetContext(ctx)
			command.SetOut(&output)
			command.SetErr(&output)
			if scenario == "cancel" {
				err = command.Execute()
			} else {
				var text string
				text, err = f.runCLI(t, current, "restore", "--input", input)
				output.WriteString(text)
			}
			want, count := "backup", 1
			if scenario == "success" {
				if err != nil || !strings.Contains(output.String(), "恢复已完成") {
					t.Fatalf("真实恢复失败：%v", err)
				}
				for _, name := range []string{"scert.json", "master.key", "secrets.json"} {
					assertRealPostgresFile(t, filepath.Join(cfg.Paths.Config, name), filepath.Join(input, name))
				}
				assertRealPostgresFile(t, certificate, filepath.Join(input, "certificates", "integration.pem"))
			} else {
				want, count = "current", 2
				if err == nil || strings.Contains(output.String(), "恢复已完成") || strings.Contains(err.Error(), f.password) {
					t.Fatal("受控失败不得误报成功或泄漏密码")
				}
				if scenario == "permission" && !strings.Contains(err.Error(), "数据库自动回滚失败") {
					t.Fatal("恢复及反向导入权限不足时必须提示人工处理")
				}
				if scenario == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("取消场景必须到达真实导入后的取消检查")
				}
				if scenario == "certificate" && !strings.Contains(err.Error(), "恢复敏感配置或证书失败") {
					t.Fatal("证书失败场景必须到达数据库导入后的资源恢复阶段")
				}
				if scenario == "inventory" && !strings.Contains(err.Error(), "对象清单不一致") {
					t.Fatal("额外对象必须由归档清单检查拦截")
				}
				for name, expected := range before {
					actual, readErr := os.ReadFile(filepath.Join(cfg.Paths.Config, name))
					if readErr != nil || !bytes.Equal(actual, expected) {
						t.Fatalf("失败恢复改变了当前文件：%s", name)
					}
				}
			}
			var value, owner string
			var rows, next int
			var allowed bool
			if err := conn.QueryRow(t.Context(), "SELECT value FROM settings WHERE key = 'integration'").Scan(&value); err != nil || value != want {
				t.Fatal("真实数据库内容不符合恢复或回滚结果")
			}
			if err := conn.QueryRow(t.Context(), "SELECT count(*) FROM audit_logs").Scan(&rows); err != nil || rows != count {
				t.Fatal("真实审计记录未正确恢复")
			}
			if err := conn.QueryRow(t.Context(), "SELECT nextval(pg_get_serial_sequence('audit_logs', 'id'))").Scan(&next); err != nil || next != count+1 {
				t.Fatal("真实自增序列未正确恢复")
			}
			if err := conn.QueryRow(t.Context(), "SELECT tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename = 'settings'").Scan(&owner); err != nil || owner != "scert_owner" {
				t.Fatal("对象所有者未保留")
			}
			if err := conn.QueryRow(t.Context(), "SELECT has_table_privilege('scert_reader', 'settings', 'SELECT')").Scan(&allowed); err != nil || !allowed {
				t.Fatal("对象授权未保留")
			}
			if scenario == "permission" {
				if err := conn.QueryRow(t.Context(), "SELECT value FROM protected_record").Scan(&value); err != nil || value != "preserved" {
					t.Fatal("权限失败不能改变受保护表")
				}
			}
			snapshots, err := filepath.Glob(filepath.Join(cfg.Paths.Backup, "scert-postgres-before-restore-*", "scert.pgdump"))
			if err != nil || len(snapshots) != 1 {
				t.Fatal("必须保留恢复前真实快照")
			}
			t.Log("真实 CLI、迁移、业务数据、序列、所有者、授权及恢复保护检查通过")
		})
	}
}

func assertRealPostgresFile(t *testing.T, actual, expected string) {
	t.Helper()
	left, leftErr := os.ReadFile(actual)
	right, rightErr := os.ReadFile(expected)
	if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
		t.Fatal("真实恢复后的文件与备份不一致")
	}
}

// 保持测试辅助函数与运行时连接校验使用相同的结构化 URL 语义。
func postgresIntegrationCA(t *testing.T, dsn, ca string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("测试连接解析失败")
	}
	query := parsed.Query()
	query.Set("sslrootcert", ca)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
