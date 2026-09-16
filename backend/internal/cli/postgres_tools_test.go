package cli

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestPostgresToolConnectionKeepsSecretsOutOfArguments(t *testing.T) {
	t.Setenv("PGHOST", "unauthorized.example.test")
	t.Setenv("PGSERVICE", "unexpected-service")
	t.Setenv("PGOPTIONS", "-c search_path=unexpected")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGPASSWORD", "unexpected-password")
	const password = "p@ss:' /\\\nword"
	parsed, _ := url.Parse("postgres://db.example.test:5432/scert?sslmode=verify-full&application_name=scert")
	parsed.User = url.UserPassword("backup-user", password)
	dsn, env, err := postgresToolConnection(config.DatabaseConfig{Driver: "postgres", DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dsn, password) || strings.Contains(dsn, "backup-user") {
		t.Fatal("账号密码不能进入命令行连接参数")
	}
	target, err := url.Parse(dsn)
	if err != nil || target.Host != "db.example.test:5432" || target.Path != "/scert" || target.User != nil {
		t.Fatal("维护连接改变了数据库目标")
	}
	for key, value := range map[string]string{"sslmode": "verify-full", "sslrootcert": "system", "connect_timeout": "10", "gssencmode": "disable"} {
		if target.Query().Get(key) != value {
			t.Fatalf("维护连接参数未保留安全值：%s", key)
		}
	}
	for _, entry := range env {
		if strings.HasPrefix(entry, "PGHOST=") || strings.HasPrefix(entry, "PGSERVICE=") || strings.HasPrefix(entry, "PGOPTIONS=") || strings.Contains(entry, "unexpected-password") {
			t.Fatal("工具不应继承外部 PostgreSQL 连接环境")
		}
	}
	if !slices.Contains(env, "PGUSER=backup-user") || !slices.Contains(env, "PGPASSWORD="+password) || !slices.Contains(env, "PGPASSFILE="+os.DevNull) {
		t.Fatal("子进程专属凭据环境不完整")
	}
}

func TestPostgresToolConnectionRejectsAmbiguousParameters(t *testing.T) {
	for _, raw := range []string{
		"postgres://owner:password@db.test/scert?sslmode=disable",
		"postgres://owner:password@db.test/scert?sslmode=verify-full&password=override",
		"postgres://owner:password@db.test/scert?sslmode=verify-full&host=other.test",
		"postgres://owner:password@db.test/scert?sslmode=verify-full&options=-c+search_path%3Dother",
		"postgres://owner:password@db.test/scert?sslmode=verify-full&service=other",
		"postgres://owner:p%00word@db.test/scert?sslmode=verify-full",
		"postgres://db.test/scert?sslmode=verify-full",
	} {
		if _, _, err := postgresToolConnection(config.DatabaseConfig{Driver: "postgres", DSN: raw}); err == nil {
			t.Fatal("不安全或语义不一致的维护连接未被拒绝")
		}
	}
}

func TestPostgresToolConnectionPreservesExplicitTLSFiles(t *testing.T) {
	parsed, _ := url.Parse("postgres://owner:password@db.test/scert?sslmode=verify-full")
	query := parsed.Query()
	for key, value := range map[string]string{"sslrootcert": "/secure/ca bundle.pem", "sslcert": "/secure/client.pem", "sslkey": "/secure/client.key", "connect_timeout": "7"} {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	dsn, _, err := postgresToolConnection(config.DatabaseConfig{Driver: "postgres", DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := url.Parse(dsn)
	for key, value := range query {
		if actual.Query().Get(key) != value[0] {
			t.Fatalf("维护连接改变了显式 TLS 或超时配置：%s", key)
		}
	}
}

func TestPostgresToolOutputLimitAppliesToCopy(t *testing.T) {
	output := &limitedPostgresOutput{remaining: 4}
	if _, err := io.Copy(output, strings.NewReader("output-over-limit")); err == nil {
		t.Fatal("工具输出不能通过流式复制绕过容量限制")
	}
	if len(output.String()) > 4 {
		t.Fatal("超限输出不应保留在内存中")
	}
}

func TestPostgresToolsRequireCompatibleVersions(t *testing.T) {
	for _, pair := range [][2]string{{"16.4", "16.9"}, {"15.9", "15.9"}, {"16.4", "17.2"}, {"invalid", "16.4"}} {
		t.Run(strings.Join(pair[:], "-"), func(t *testing.T) {
			tools := &postgresTools{dumpPath: "pg_dump", restorePath: "pg_restore"}
			tools.run = func(ctx context.Context, path string, args, env []string, output io.Writer) error {
				if ctx.Err() != nil || !slices.Equal(args, []string{"--version"}) {
					t.Fatal("版本检查参数无效")
				}
				version := pair[0]
				if path == "pg_restore" {
					version = pair[1]
				}
				_, err := io.WriteString(output, path+" (PostgreSQL) "+version+"\n")
				return err
			}
			err := tools.validateVersions(t.Context())
			if (err == nil) != (pair[0] == "16.4" && pair[1] == "16.9") {
				t.Fatalf("版本检查结果不符：%v", err)
			}
		})
	}
}

func TestPostgresMissingToolsFailBeforeBackupFilesystemChanges(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := loadPostgresTools(t.Context()); err == nil {
		t.Fatal("没有官方工具时必须明确拒绝")
	}
	root := t.TempDir()
	output := filepath.Join(root, "missing", "backup")
	err := createBackup(t.Context(), config.Config{Database: config.DatabaseConfig{Driver: "postgres"}}, "unused.json", output)
	if err == nil {
		t.Fatal("缺少工具时不能生成备份")
	}
	if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
		t.Fatal("工具检查失败前不应创建输出目录")
	}
}

func TestPostgresToolHelperProcess(t *testing.T) {
	mode := os.Getenv("SCERT_POSTGRES_TOOL_TEST")
	if mode == "" {
		return
	}
	if mode == "cancel" {
		time.Sleep(30 * time.Second)
	} else {
		_, _ = io.WriteString(os.Stderr, "sensitive-database-value")
		os.Exit(7)
	}
}

func TestPostgresToolExecutionRedactsErrorsAndCancels(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			err := executePostgresTool(ctx, executable, []string{"-test.run=^TestPostgresToolHelperProcess$"}, append(postgresToolEnvironment(), "SCERT_POSTGRES_TOOL_TEST="+mode), io.Discard)
			if err == nil || strings.Contains(err.Error(), "sensitive-database-value") || strings.Contains(err.Error(), executable) {
				t.Fatal("工具错误必须脱敏并保持失败状态")
			}
			if mode == "cancel" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("超时必须终止工具并返回取消原因")
			}
		})
	}
}
