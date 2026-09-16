package database

import (
	"context"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"github.com/jackc/pgx/v5"
)

type countedCredentialReader struct {
	calls int
}

func (reader *countedCredentialReader) Get(context.Context, string) (map[string]string, error) {
	reader.calls++
	return map[string]string{"username": "expected-user", "password": "expected-secret"}, nil
}

func TestPostgresQueryCannotOverrideEncryptedCredentials(t *testing.T) {
	for _, query := range []string{
		"password=untrusted-secret", "user=untrusted-user", "pass%77ord=untrusted-secret",
		"sslpassword=untrusted-secret", "host=other.example.test", "dbname=other",
		"sslmode=disable", "service=other", "passfile=/tmp/passwords",
	} {
		t.Run(query, func(t *testing.T) {
			cfg := config.DatabaseConfig{
				Driver: "postgres", DSN: "postgres://db.example.test:5432/scert?sslmode=verify-full&" + query,
				CredentialRef: "secret://database/test",
			}
			reader := &countedCredentialReader{}
			if _, err := ResolveConfig(t.Context(), cfg, reader); err == nil {
				t.Fatal("连接参数覆盖没有在读取凭据前被拒绝")
			}
			if reader.calls != 0 {
				t.Fatal("校验无效连接参数不应读取加密凭据")
			}
		})
	}
}

func TestPostgresRuntimeDSNRejectsOverridesAndHidesParseErrors(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user:LEAKME@db.example.test/scert?sslmode=verify-full&password=override",
		"postgres://user:LEAKME@db.example.test/scert?sslmode=verify-full&host=other.example.test",
		"postgres://user:LEAKME@db.example.test/scert?sslmode=verify-full&sslmode=disable",
		"postgres://user:LEAKME@db.example.test/scert?sslmode=verify-full&connect_timeout=invalid",
		"postgres://user:LEAKME%zz@db.example.test/scert?sslmode=verify-full",
		"host=db.example.test user=user password='LEAKME' sslmode=verify-full port=invalid",
	} {
		result, err := normalizePostgresDSN(dsn)
		if err == nil || result != "" {
			t.Fatal("无效的运行时 PostgreSQL 连接信息未被拒绝")
		}
		if strings.Contains(err.Error(), "LEAKME") {
			t.Fatal("解析错误泄露了运行时密码")
		}
	}
}

func TestPostgresDriverQueryPrecedenceExplainsSecurityBoundary(t *testing.T) {
	parsed, err := pgx.ParseConfig("postgres://expected-user:expected-secret@db.example.test/scert?sslmode=verify-full&user=overridden&password=overridden")
	if err != nil {
		t.Fatal("驱动测试夹具无效")
	}
	if parsed.User != "overridden" || parsed.Password != "overridden" {
		t.Fatal("驱动参数优先级已变化，应重新复审防护边界")
	}
}

func TestPostgresResolvedCredentialsMatchDriver(t *testing.T) {
	cfg := config.DatabaseConfig{
		Driver: "postgres", DSN: "postgres://db.example.test:5432/scert?sslmode=verify-full&application_name=scert",
		CredentialRef: "secret://database/test",
	}
	const username = "user@example.test"
	const password = "space @:/?#&% quoted'\\ secret"
	resolved, err := ResolveConfig(t.Context(), cfg, memoryCredentialReader{
		cfg.CredentialRef: {"username": username, "password": password},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := pgx.ParseConfig(resolved.DSN)
	if err != nil {
		t.Fatal("驱动无法解析有效内存凭据")
	}
	if parsed.User != username || parsed.Password != password || parsed.Host != "db.example.test" ||
		parsed.Database != "scert" || parsed.TLSConfig == nil || parsed.TLSConfig.InsecureSkipVerify {
		t.Fatal("驱动解析结果与受保护账号或验证目标不一致")
	}
}

func TestPostgresSanitizationDoesNotLeakNonUserinfoSecrets(t *testing.T) {
	for _, dsn := range []string{
		"postgres://db.example.test/scert?password=LEAKME&sslmode=verify-full",
		"postgres://db.example.test/scert?pass%77ord=LEAKME&sslmode=verify-full",
		"postgres://db.example.test/scert?password=first&password=LEAKME",
		"postgres://db.example.test/scert?sslpassword=LEAKME",
		"postgres://db.example.test/scert?application_name=%zz&password=LEAKME",
		"postgres://user:bad%zzLEAKME@db.example.test/scert",
		"postgres://db.example.test/scert#LEAKME",
		"host=db.example.test password='first LEAKME' dbname=scert",
		"host=db.example.test password = 'LEAKME' dbname=scert",
		"host=db.example.test password='first \\' LEAKME'",
	} {
		if result := SanitizeDSN("postgres", dsn); result == "" || strings.Contains(result, "LEAKME") {
			t.Fatal("PostgreSQL 脱敏未覆盖查询参数、无效编码或带空格的密码")
		}
	}
}
