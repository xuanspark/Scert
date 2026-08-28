package database

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type Database struct {
	DB *sql.DB
}

func Open(ctx context.Context, cfg config.DatabaseConfig) (*Database, error) {
	if cfg.Driver != "sqlite" && cfg.Driver != "mysql" {
		return nil, fmt.Errorf("数据库驱动无效：%s", cfg.Driver)
	}
	dsn := cfg.DSN
	if cfg.Driver == "mysql" {
		var err error
		dsn, err = normalizeMySQLDSN(dsn)
		if err != nil {
			return nil, err
		}
	}
	if cfg.Driver == "sqlite" {
		if cfg.DSN != ":memory:" && len(cfg.DSN) > 0 && cfg.DSN != "file::memory:?cache=shared" {
			if err := ensureSQLiteDir(cfg.DSN); err != nil {
				return nil, err
			}
		}
	}
	db, err := sql.Open(cfg.Driver, dsn)
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
	if cfg.Driver == "sqlite" {
		if err := configureSQLite(pingCtx, db); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &Database{DB: db}, nil
}

func configureSQLite(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("配置 SQLite 忙等待失败：%w", err)
	}
	var mode string
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		return fmt.Errorf("配置 SQLite WAL 失败：%w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("读取 SQLite 日志模式失败：%w", err)
	}
	if strings.EqualFold(strings.TrimSpace(mode), "memory") || strings.EqualFold(strings.TrimSpace(mode), "wal") {
		return nil
	}
	return fmt.Errorf("SQLite 日志模式无效：%s", mode)
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

// SanitizeDSN 返回可写入日志的数据库连接信息，不包含 MySQL 密码。
func SanitizeDSN(driver, dsn string) string {
	if strings.EqualFold(strings.TrimSpace(driver), "mysql") {
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			return "mysql://[连接信息无效]"
		}
		if cfg.Passwd != "" {
			cfg.Passwd = "********"
		}
		return cfg.FormatDSN()
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
