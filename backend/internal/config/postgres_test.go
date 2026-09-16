package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresUnsafeDSNCannotBeSaved(t *testing.T) {
	const base = "postgres://db.example.test:5432/scert?sslmode=verify-full"
	for _, suffix := range []string{
		"&password=unsafe-secret", "&pass%77ord=unsafe-secret", "&user=other-user",
		"&host=other.example.test", "&port=5433", "&dbname=other", "&database=other",
		"&service=other", "&servicefile=/tmp/service", "&passfile=/tmp/passwords",
		"&sslpassword=unsafe-secret", "&sslmode=disable", "&application_name=%zz",
		"#unsafe-secret", "&Password=unsafe-secret",
	} {
		t.Run(suffix, func(t *testing.T) {
			cfg := Default(t.TempDir())
			cfg.Database.Driver = "postgres"
			cfg.Database.DSN = base + suffix
			cfg.Database.CredentialRef = "secret://database/test"
			path := filepath.Join(t.TempDir(), "scert.json")
			err := cfg.Save(path)
			if err == nil {
				t.Fatal("含凭据或连接覆盖参数的配置未被拒绝")
			}
			if strings.Contains(err.Error(), "unsafe-secret") {
				t.Fatal("配置错误泄露了输入凭据")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("无效配置不应写入磁盘")
			}
		})
	}
}

func TestPostgresSafeConfigRoundTrips(t *testing.T) {
	for _, dsn := range []string{
		"postgres://db.example.test:5432/scert?sslmode=verify-full",
		"postgresql://[2001:db8::10]:5432/scert?sslmode=verify-full&application_name=scert&connect_timeout=10",
		"postgres://db.example.test/scert?sslmode=verify-full&sslrootcert=%2Fetc%2Fscert%2Froot.pem",
	} {
		cfg := Default(t.TempDir())
		cfg.Database.Driver = "postgres"
		cfg.Database.DSN = dsn
		cfg.Database.CredentialRef = "secret://database/test"
		path := filepath.Join(t.TempDir(), "scert.json")
		if err := cfg.Save(path); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path)
		if err != nil || loaded.Database != cfg.Database {
			t.Fatal("合法 PostgreSQL 连接配置不能保存并重新加载")
		}
	}
}

func FuzzValidatePostgresDSN(f *testing.F) {
	for _, dsn := range []string{
		"postgres://db.example.test/scert?sslmode=verify-full",
		"postgres://db.example.test/scert?sslmode=verify-full&password=fuzz-secret",
		"postgres://db.example.test/scert?sslmode=verify-full&sslmode=disable",
		"postgres://db.example.test/scert?sslmode=verify-full&password=%zz",
	} {
		f.Add(dsn)
	}
	f.Fuzz(func(t *testing.T, dsn string) {
		err := ValidatePostgresDSN(dsn)
		if err != nil && strings.Contains(err.Error(), "fuzz-secret") {
			t.Fatal("校验错误不能包含输入凭据")
		}
	})
}
