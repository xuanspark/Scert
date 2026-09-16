package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

func TestUnsupportedBackupDriverFailsBeforeFilesystemChanges(t *testing.T) {
	for _, driver := range []string{"postgres", "unknown", ""} {
		t.Run(driver, func(t *testing.T) {
			root := t.TempDir()
			cfg := config.Config{Database: config.DatabaseConfig{Driver: driver}}
			output := filepath.Join(root, "not-created", "backup")
			err := createBackup(t.Context(), cfg, filepath.Join(root, "scert.json"), output)
			if err == nil || !strings.Contains(err.Error(), "备份") {
				t.Fatal("不支持的数据库不能报告备份成功")
			}
			if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
				t.Fatal("拒绝备份前不应创建输出目录")
			}
		})
	}
}

func TestPostgresRestoreFailsBeforeReplacingExistingConfig(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	if err := os.Mkdir(input, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(root)
	// 显式覆盖路径，避免 Linux 测试使用正式安装目录。
	cfg.Paths = config.PathsConfig{
		Install: root, Config: filepath.Join(root, "conf"), Data: filepath.Join(root, "data"),
		Certificate: filepath.Join(root, "ssl"), Log: filepath.Join(root, "log"), Backup: filepath.Join(root, "backups"),
	}
	cfg.Database = config.DatabaseConfig{
		Driver: "postgres", DSN: "postgres://db.example.test:5432/scert?sslmode=verify-full",
		CredentialRef: "secret://database/restore-test",
		MaxOpenConns:  4, MaxIdleConns: 2, ConnMaxLifetimeMins: 30,
	}
	if err := cfg.Save(filepath.Join(input, "scert.json")); err != nil {
		t.Fatal(err)
	}
	if err := writeBackupManifest(input); err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(root, "current.json")
	const original = "preserve-current-config"
	if err := os.WriteFile(currentPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := configPath
	configPath = currentPath
	t.Cleanup(func() { configPath = previous })
	command := newRestoreCommand()
	var output strings.Builder
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--input", input})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "PostgreSQL 完整备份与恢复尚未实现") {
		t.Fatal("未支持的 PostgreSQL 恢复没有明确拒绝")
	}
	content, err := os.ReadFile(currentPath)
	if err != nil || string(content) != original {
		t.Fatal("拒绝恢复时改变了现有配置")
	}
	if _, err := os.Stat(cfg.Paths.Data); !os.IsNotExist(err) {
		t.Fatal("拒绝恢复前不应创建锁目录或数据库")
	}
}

func TestMaintenanceResolvesEncryptedMySQLCredentialsOnlyInMemory(t *testing.T) {
	directory := t.TempDir()
	store, err := secretstore.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DatabaseConfig{
		Driver: "mysql", DSN: "tcp(db.example.test:3306)/scert?tls=true",
		CredentialRef: "secret://database/maintenance-test",
	}
	const password = "maintenance-test-password"
	if err := store.Put(t.Context(), cfg.CredentialRef, map[string]string{"username": "backup-user", "password": password}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(directory, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveMaintenanceDatabase(t.Context(), cfg, directory)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mysql.ParseDSN(resolved.DSN)
	if err != nil || parsed.User != "backup-user" || parsed.Passwd != password || parsed.TLSConfig != "true" {
		t.Fatal("维护连接没有正确补全加密凭据或保留 TLS 设置")
	}
	if strings.Contains(cfg.DSN, "backup-user") || strings.Contains(cfg.DSN, password) {
		t.Fatal("原始配置被写入数据库账号密码")
	}
	after, err := os.ReadFile(filepath.Join(directory, "secrets.json"))
	if err != nil || !bytes.Equal(before, after) || bytes.Contains(after, []byte(password)) {
		t.Fatal("维护连接改变了敏感文件或泄露明文密码")
	}
}

func TestBackupValidatesDatabaseCredentialReference(t *testing.T) {
	directory := t.TempDir()
	if err := createTestSecretStore(directory); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Database: config.DatabaseConfig{CredentialRef: "secret://database/missing"}}
	if err := validateBackupSecretStore(t.Context(), directory, cfg); err == nil {
		t.Fatal("备份不能忽略缺失的数据库加密凭据")
	}
	if _, err := resolveMaintenanceDatabase(t.Context(), config.DatabaseConfig{
		Driver: "mysql", DSN: "tcp(db.example.test:3306)/scert?tls=true", CredentialRef: cfg.Database.CredentialRef,
	}, directory); err == nil || strings.Contains(err.Error(), cfg.Database.CredentialRef) {
		t.Fatal("维护连接缺失凭据时必须失败且不泄露完整引用")
	}
}
