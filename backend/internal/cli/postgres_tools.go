package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type postgresToolRunner func(context.Context, string, []string, []string, io.Writer) error
type postgresToolsFactory func(context.Context) (*postgresTools, error)

type postgresTools struct {
	dumpPath    string
	restorePath string
	run         postgresToolRunner
}

var postgresVersionPattern = regexp.MustCompile(`^pg_(?:dump|restore) \(PostgreSQL\) (\d+)\.`)

func loadPostgresTools(ctx context.Context) (*postgresTools, error) {
	dump, err := exec.LookPath("pg_dump")
	if err != nil {
		return nil, errors.New("PostgreSQL 备份需要安装官方 pg_dump 和 pg_restore（相同主版本，16 或更高），并加入 PATH")
	}
	restore, err := exec.LookPath("pg_restore")
	if err != nil {
		return nil, errors.New("PostgreSQL 恢复及备份校验需要 pg_restore，请安装与 pg_dump 相同主版本的官方工具")
	}
	tools := &postgresTools{dumpPath: dump, restorePath: restore, run: executePostgresTool}
	if err := tools.validateVersions(ctx); err != nil {
		return nil, err
	}
	return tools, nil
}

func executePostgresTool(ctx context.Context, path string, args, env []string, output io.Writer) error {
	command := exec.CommandContext(ctx, path, args...)
	command.Env = env
	command.Stdout = output
	// 工具可能在错误中回显数据库内容或连接信息，不捕获、不透传原始输出。
	command.Stderr = io.Discard
	command.WaitDelay = 2 * time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("PostgreSQL 工具执行失败，请检查工具版本、TLS 证书、数据库权限、连接及备份完整性；原始输出已隐藏")
	}
	return nil
}

// 清除继承的连接设置，避免 PGHOST、PGSERVICE 等环境变量改变目标或降低 TLS。
func postgresToolEnvironment() []string {
	env := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "PG") || key == "LC_ALL" || key == "LANG" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "LC_ALL=C", "LANG=C", "PGPASSFILE="+os.DevNull, "PGSERVICEFILE="+os.DevNull)
}

func postgresToolConnection(cfg config.DatabaseConfig) (string, []string, error) {
	parsed, err := url.Parse(strings.TrimSpace(cfg.DSN))
	if err != nil || cfg.Driver != "postgres" || parsed.User == nil {
		return "", nil, errors.New("PostgreSQL 维护连接无效或未解析加密账号")
	}
	username := parsed.User.Username()
	password, exists := parsed.User.Password()
	if username == "" || !exists || password == "" || strings.ContainsRune(username+password, 0) {
		return "", nil, errors.New("PostgreSQL 维护账号或密码无效")
	}
	parsed.User = nil
	if err := config.ValidatePostgresDSN(parsed.String()); err != nil {
		return "", nil, errors.New("PostgreSQL 维护连接参数不满足安全要求")
	}
	query := parsed.Query()
	// 只接受已确认在 pgx 和 libpq 中具有一致连接语义的参数。
	for key := range query {
		switch key {
		case "sslmode", "sslrootcert", "sslcert", "sslkey", "connect_timeout", "application_name":
		default:
			return "", nil, errors.New("PostgreSQL 维护连接包含不支持的参数，请仅使用 TLS 证书、连接超时及应用名称设置")
		}
	}
	if query.Get("sslrootcert") == "" {
		// libpq 16 起支持系统信任库；不同平台的库来源仍需实机核对，不能降级 TLS。
		query.Set("sslrootcert", "system")
	}
	if query.Get("connect_timeout") == "" {
		query.Set("connect_timeout", "10")
	}
	query.Set("gssencmode", "disable")
	parsed.RawQuery = query.Encode()
	env := append(postgresToolEnvironment(), "PGUSER="+username, "PGPASSWORD="+password)
	return parsed.String(), env, nil
}

func (tools *postgresTools) validateVersions(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	major := 0
	for _, path := range []string{tools.dumpPath, tools.restorePath} {
		output := &limitedPostgresOutput{remaining: 4096}
		if err := tools.run(ctx, path, []string{"--version"}, postgresToolEnvironment(), output); err != nil {
			return errors.New("无法读取 PostgreSQL 工具版本，请检查 PATH 和可执行文件")
		}
		match := postgresVersionPattern.FindStringSubmatch(strings.TrimSpace(output.String()))
		if len(match) != 2 {
			return errors.New("无法识别 PostgreSQL 工具版本")
		}
		version, err := strconv.Atoi(match[1])
		if err != nil || version < 16 || (major != 0 && major != version) {
			return errors.New("pg_dump 和 pg_restore 必须具有相同主版本且不低于 16；pg_dump 也不得低于数据库服务端主版本")
		}
		major = version
	}
	return nil
}

type limitedPostgresOutput struct {
	buffer    bytes.Buffer
	remaining int
}

func (output *limitedPostgresOutput) String() string { return output.buffer.String() }

func (output *limitedPostgresOutput) Write(value []byte) (int, error) {
	if len(value) > output.remaining {
		return 0, fmt.Errorf("PostgreSQL 工具输出超过安全上限")
	}
	output.remaining -= len(value)
	return output.buffer.Write(value)
}
