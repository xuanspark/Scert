package database

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type memoryCredentialReader map[string]map[string]string

func (m memoryCredentialReader) Get(_ context.Context, reference string) (map[string]string, error) {
	return m[reference], nil
}

func TestValidateConfigRequiresEncryptedExternalDatabaseCredentials(t *testing.T) {
	base := config.Default(t.TempDir()).Database
	for _, item := range []struct {
		name string
		cfg  config.DatabaseConfig
	}{
		{name: "SQLite cannot have credential reference", cfg: config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "scert.db"), CredentialRef: "secret://database/unexpected"}},
		{name: "MySQL requires reference", cfg: config.DatabaseConfig{Driver: "mysql", DSN: "user@tcp(db.example.test:3306)/scert"}},
		{name: "MySQL rejects password in saved DSN", cfg: config.DatabaseConfig{Driver: "mysql", DSN: "user:password@tcp(db.example.test:3306)/scert", CredentialRef: "secret://database/test"}},
		{name: "Postgres requires secure reference", cfg: config.DatabaseConfig{Driver: "postgres", DSN: "postgres://user@db.example.test:5432/scert?sslmode=verify-full", CredentialRef: "secret://dns/not-database"}},
		{name: "Postgres rejects password in saved DSN", cfg: config.DatabaseConfig{Driver: "postgres", DSN: "postgres://user:password@db.example.test:5432/scert?sslmode=verify-full", CredentialRef: "secret://database/test"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			if err := ValidateConfig(item.cfg); err == nil {
				t.Fatal("不安全数据库配置未被拒绝")
			}
		})
	}
	if err := ValidateConfig(base); err != nil {
		t.Fatalf("有效 SQLite 配置被拒绝：%v", err)
	}
}

func TestResolveConfigBuildsRuntimeDSNWithoutChangingPersistedConfig(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.DatabaseConfig
		wantSecret string
	}{
		{
			name:       "MySQL",
			cfg:        config.DatabaseConfig{Driver: "mysql", DSN: "tcp(db.example.test:3306)/scert?charset=utf8mb4&tls=true", CredentialRef: "secret://database/mysql"},
			wantSecret: "mysql-password",
		},
		{
			name:       "PostgreSQL",
			cfg:        config.DatabaseConfig{Driver: "postgres", DSN: "postgres://db.example.test:5432/scert?sslmode=verify-full", CredentialRef: "secret://database/postgres"},
			wantSecret: "postgres-password",
		},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			originalDSN := item.cfg.DSN
			resolved, err := ResolveConfig(t.Context(), item.cfg, memoryCredentialReader{
				item.cfg.CredentialRef: {"username": "user", "password": item.wantSecret},
			})
			if err != nil {
				t.Fatalf("组装运行时连接信息失败：%v", err)
			}
			if !strings.Contains(resolved.DSN, item.wantSecret) {
				t.Fatalf("运行时连接信息未包含内存凭据：%s", SanitizeDSN(item.cfg.Driver, resolved.DSN))
			}
			if item.cfg.DSN != originalDSN || strings.Contains(item.cfg.DSN, item.wantSecret) {
				t.Fatalf("持久化配置被改写或包含密码：%+v", item.cfg)
			}
		})
	}
}

func TestValidateConfigKeepsExternalDatabaseUsernameOutOfPersistedDSN(t *testing.T) {
	for _, cfg := range []config.DatabaseConfig{
		{Driver: "mysql", DSN: "user@tcp(db.example.test:3306)/scert?tls=true", CredentialRef: "secret://database/mysql"},
		{Driver: "postgres", DSN: "postgres://user@db.example.test:5432/scert?sslmode=verify-full", CredentialRef: "secret://database/postgres"},
	} {
		if err := ValidateConfig(cfg); err == nil {
			t.Fatalf("持久化配置中的数据库用户名未被拒绝：%s", cfg.Driver)
		}
	}
}

func TestVerifyWritableSQLiteCleansTemporaryTable(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "verification.db")
	if err := VerifyWritable(t.Context(), cfg); err != nil {
		t.Fatalf("SQLite 可写验证失败：%v", err)
	}
	db, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE ?`, verificationTablePrefix+"%").Scan(&count); err != nil {
		t.Fatalf("检查临时表清理失败：%v", err)
	}
	if count != 0 {
		t.Fatalf("数据库验证遗留临时表：%d", count)
	}
}

func TestNewCredentialReferenceHasDatabasePrefixAndIsUnique(t *testing.T) {
	first, err := NewCredentialReference()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCredentialReference()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "secret://database/") || first == second {
		t.Fatalf("数据库凭据引用不安全：first=%s second=%s", first, second)
	}
}
