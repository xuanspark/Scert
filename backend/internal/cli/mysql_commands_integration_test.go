package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/spf13/cobra"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

// 在同一进程调用正式命令；测试 TLS 注册名不能用于独立发行二进制验收。
func TestMySQLRealBackupRestoreCommands(t *testing.T) {
	testMySQLBackupRestoreCommands(t, "")
}

func testMySQLBackupRestoreCommands(t *testing.T, binary string) {
	server := mysqltest.New(t)
	for _, scenario := range []string{"success", "sql_failure", "certificate_failure"} {
		t.Run(scenario, func(t *testing.T) {
			connection := server.Database(t)
			connection.MaxIdleConns = 0
			db, err := database.Open(t.Context(), connection)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := database.Migrate(t.Context(), db.DB, "mysql"); err != nil {
				t.Fatal(err)
			}
			exec := func(statement string) {
				t.Helper()
				if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			exec("INSERT INTO settings (`key`, value) VALUES ('integration', 'backup')")
			exec("INSERT INTO audit_logs (action, resource_type, resource_id, status, message) VALUES ('backup', 'test', '1', 'ok', 'backup')")
			root := filepath.Join(server.Root(), scenario)
			cfg := config.Default(root)
			cfg.Paths = config.PathsConfig{
				Install: root, Config: filepath.Join(root, "conf"), Data: filepath.Join(root, "data"),
				Certificate: filepath.Join(root, "ssl"), Log: filepath.Join(root, "log"), Backup: filepath.Join(root, "snapshots"),
			}
			parsed, err := mysql.ParseDSN(connection.DSN)
			if err != nil {
				t.Fatal("解析隔离数据库连接失败")
			}
			username, password := parsed.User, parsed.Passwd
			parsed.User, parsed.Passwd = "", ""
			if binary != "" {
				parsed.TLSConfig = "true"
			}
			cfg.Database = connection
			cfg.Database.DSN = parsed.FormatDSN()
			cfg.Database.CredentialRef = "secret://database/maintenance"
			current := filepath.Join(cfg.Paths.Config, "scert.json")
			if err := cfg.Save(current); err != nil {
				t.Fatal(err)
			}
			store, err := secretstore.Open(cfg.Paths.Config)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Put(t.Context(), cfg.Database.CredentialRef, map[string]string{"username": username, "password": password}); err != nil {
				t.Fatal(err)
			}
			write := func(path string, content []byte) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			read := func(path string) []byte {
				t.Helper()
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return content
			}
			certificate := filepath.Join(cfg.Paths.Certificate, "integration.pem")
			write(certificate, read(filepath.Join(server.Root(), "server.crt")))
			previous := configPath
			configPath = current
			t.Cleanup(func() { configPath = previous })
			run := func(command *cobra.Command, args ...string) (string, error) {
				t.Helper()
				if binary != "" {
					return runMySQLReleaseCLI(t, binary, current, filepath.Join(server.Root(), "server.crt"), password, append([]string{command.Name()}, args...)...)
				}
				var output bytes.Buffer
				command.SetArgs(args)
				command.SetContext(t.Context())
				command.SetOut(&output)
				command.SetErr(&output)
				err := command.Execute()
				if strings.Contains(output.String(), password) || (err != nil && strings.Contains(err.Error(), password)) {
					t.Fatal("命令输出不得泄漏数据库密码")
				}
				return output.String(), err
			}
			input := filepath.Join(root, "backup")
			if _, err := run(newBackupCommand(), "--output", input); err != nil {
				t.Fatal(err)
			}
			if err := validateBackupManifest(input); err != nil {
				t.Fatal(err)
			}
			exec("UPDATE settings SET value = 'current' WHERE `key` = 'integration'")
			exec("INSERT INTO audit_logs (action, resource_type, resource_id, status, message) VALUES ('current', 'test', '2', 'ok', 'current')")
			cfg.Server.Port++
			if err := cfg.Save(current); err != nil {
				t.Fatal(err)
			}
			if err := store.Put(t.Context(), "secret://integration/marker", map[string]string{"value": "current"}); err != nil {
				t.Fatal(err)
			}
			write(certificate, []byte("current-certificate"))
			before := make(map[string][]byte)
			for _, name := range []string{"scert.json", "master.key", "secrets.json"} {
				before[name] = read(filepath.Join(cfg.Paths.Config, name))
			}
			if scenario == "sql_failure" {
				// 保持清单有效，以确认错误发生在实际导入阶段而非哈希校验阶段。
				path := filepath.Join(input, "scert.sql")
				write(path, append(read(path), []byte("\nINSERT INTO missing_restore_table VALUES (1);\n")...))
				manifest, err := buildBackupManifestExcludingManifest(input)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(input, "manifest.json"), encoded)
				if err := validateBackupManifest(input); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "certificate_failure" {
				if err := os.Rename(cfg.Paths.Certificate, cfg.Paths.Certificate+"-preserved"); err != nil {
					t.Fatal(err)
				}
				write(cfg.Paths.Certificate, []byte("blocked-target"))
			}
			output, err := run(newRestoreCommand(), "--input", input)
			want, count := "backup", 1
			if scenario == "success" {
				if err != nil || !strings.Contains(output, "恢复已完成") {
					t.Fatalf("正式命令恢复失败：%v", err)
				}
				for name := range before {
					if !bytes.Equal(read(filepath.Join(cfg.Paths.Config, name)), read(filepath.Join(input, name))) {
						t.Fatalf("未恢复备份配置：%s", name)
					}
				}
				if !bytes.Equal(read(certificate), read(filepath.Join(input, "certificates", "integration.pem"))) {
					t.Fatal("未恢复证书文件")
				}
			} else {
				want, count = "current", 2
				phase := "恢复 MySQL 数据库失败"
				if scenario == "certificate_failure" {
					phase = "恢复敏感配置或证书失败"
				}
				if err == nil || !strings.Contains(err.Error(), phase) || strings.Contains(output, "恢复已完成") {
					t.Fatalf("未在指定阶段失败：%v", err)
				}
				for name, content := range before {
					if !bytes.Equal(read(filepath.Join(cfg.Paths.Config, name)), content) {
						t.Fatalf("失败后未恢复当前配置：%s", name)
					}
				}
				preserved := certificate
				if scenario == "certificate_failure" {
					preserved = filepath.Join(cfg.Paths.Certificate+"-preserved", "integration.pem")
				}
				if string(read(preserved)) != "current-certificate" {
					t.Fatal("失败后原证书内容被修改")
				}
			}
			var value string
			var rows int
			if err := db.DB.QueryRowContext(t.Context(), "SELECT value FROM settings WHERE `key` = 'integration'").Scan(&value); err != nil || value != want {
				t.Fatalf("恢复或回滚后的业务数据错误：%v", err)
			}
			if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_logs").Scan(&rows); err != nil || rows != count {
				t.Fatalf("恢复或回滚后的审计记录错误：%v", err)
			}
			snapshots, err := filepath.Glob(filepath.Join(cfg.Paths.Backup, "scert-before-restore-*", "scert.sql"))
			if err != nil || len(snapshots) != 1 {
				t.Fatal("必须保留恢复前的数据库快照")
			}
		})
	}
}
