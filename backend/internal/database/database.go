package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type Database struct {
	DB *sql.DB
}

func Open(ctx context.Context, cfg config.DatabaseConfig) (*Database, error) {
	if err := ValidateConfigForOpen(cfg); err != nil {
		return nil, err
	}
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver != "sqlite" && driver != "mysql" && driver != "postgres" {
		return nil, fmt.Errorf("数据库驱动无效：%s", cfg.Driver)
	}
	dsn := strings.TrimSpace(cfg.DSN)
	if driver == "mysql" {
		var err error
		dsn, err = normalizeMySQLDSN(dsn)
		if err != nil {
			return nil, err
		}
	}
	if driver == "postgres" {
		var err error
		dsn, err = normalizePostgresDSN(dsn)
		if err != nil {
			return nil, err
		}
	}
	if driver == "sqlite" {
		if cfg.DSN != ":memory:" && len(cfg.DSN) > 0 && cfg.DSN != "file::memory:?cache=shared" {
			if err := ensureSQLiteDir(cfg.DSN); err != nil {
				return nil, err
			}
		}
	}
	sqlDriver := driver
	if driver == "postgres" {
		sqlDriver = "pgx"
	}
	var db *sql.DB
	var err error
	if driver == "mysql" {
		db, err = openMySQLPool(dsn)
	} else if driver == "sqlite" {
		db = openSQLitePool(dsn)
	} else {
		db, err = sql.Open(sqlDriver, dsn)
	}
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败：%w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetimeMins) * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库失败：%w", err)
	}
	return &Database{DB: db}, nil
}

// ValidateConfigForOpen 接受已在内存中补全凭据的外部数据库配置。
// 持久化配置必须使用 ValidateConfig；运行时配置由 ResolveConfig 生成。
func ValidateConfigForOpen(cfg config.DatabaseConfig) error {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver != "sqlite" && driver != "mysql" && driver != "postgres" {
		return fmt.Errorf("数据库驱动无效：%s", cfg.Driver)
	}
	if strings.TrimSpace(cfg.DSN) == "" {
		return fmt.Errorf("数据库连接信息不能为空")
	}
	if driver == "sqlite" && strings.TrimSpace(cfg.CredentialRef) != "" {
		return fmt.Errorf("SQLite 不允许配置数据库凭据引用")
	}
	return nil
}

func normalizeMySQLDSN(dsn string) (string, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("解析 MySQL 连接信息失败：%w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	return cfg.FormatDSN(), nil
}

func normalizePostgresDSN(dsn string) (string, error) {
	// 运行时账号位于 URL 的用户信息中，其他参数仍必须满足持久化安全边界。
	if raw, err := url.Parse(dsn); err == nil && (raw.Scheme == "postgres" || raw.Scheme == "postgresql") {
		raw.User = nil
		if err := config.ValidatePostgresDSN(raw.String()); err != nil {
			return "", err
		}
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		// 驱动错误可能包含原始连接串，不能把尚未脱敏的输入带到日志或终端。
		return "", fmt.Errorf("解析 PostgreSQL 连接信息失败，请检查连接参数和证书文件")
	}
	if strings.TrimSpace(parsed.Host) == "" || strings.TrimSpace(parsed.Database) == "" || strings.TrimSpace(parsed.User) == "" {
		return "", fmt.Errorf("PostgreSQL 连接信息必须包含主机、数据库和用户名")
	}
	sslMode := postgresSSLMode(dsn)
	if sslMode != "verify-full" {
		return "", fmt.Errorf("PostgreSQL 连接必须使用 sslmode=verify-full")
	}
	if parsed.TLSConfig == nil || parsed.TLSConfig.InsecureSkipVerify {
		return "", fmt.Errorf("PostgreSQL TLS 校验配置无效")
	}
	return dsn, nil
}

func postgresSSLMode(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if parsed, err := url.Parse(dsn); err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		return strings.ToLower(strings.TrimSpace(parsed.Query().Get("sslmode")))
	}
	for _, field := range strings.Fields(dsn) {
		key, value, found := strings.Cut(field, "=")
		if found && strings.EqualFold(strings.TrimSpace(key), "sslmode") {
			return strings.ToLower(strings.Trim(strings.TrimSpace(value), "'\""))
		}
	}
	return ""
}

// SanitizeDSN 返回可写入日志的数据库连接信息，不包含数据库密码。
func SanitizeDSN(driver, dsn string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "mysql":
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			return "mysql://[连接信息无效]"
		}
		if cfg.Passwd != "" {
			cfg.Passwd = "********"
		}
		return cfg.FormatDSN()
	case "postgres":
		// 关键字串支持空格和转义，URL 查询也可以承载凭据。
		// 不尝试在任意输入中定位密码，整体隐藏，避免无效编码或新参数导致漏脱敏。
		return "postgres://[连接信息已隐藏]"
	}
	return dsn
}

func ensureSQLiteDir(dsn string) error {
	path := dsn
	if strings.HasPrefix(path, "file:") {
		path = strings.TrimPrefix(strings.SplitN(path, "?", 2)[0], "file:")
	}
	return osMkdirAll(filepath.Dir(path))
}

var osMkdirAll = func(path string) error {
	if path == "." || path == "" {
		return nil
	}
	return mkdirAll(path)
}

var mkdirAll = func(path string) error {
	return makeDirectory(path)
}

func (d *Database) Close() error {
	if d == nil || d.DB == nil {
		return nil
	}
	return d.DB.Close()
}

// Ping 检查数据库连接是否可用。
func (d *Database) Ping(ctx context.Context) error {
	if d == nil || d.DB == nil {
		return fmt.Errorf("数据库连接不能为空")
	}
	if err := d.DB.PingContext(ctx); err != nil {
		return fmt.Errorf("数据库健康检查失败：%w", err)
	}
	return nil
}
