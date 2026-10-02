package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func openSQLiteTestPool(t *testing.T, cfg config.DatabaseConfig) *sql.DB {
	t.Helper()
	db, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("打开隔离 SQLite 文件库失败：%v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("关闭隔离 SQLite 连接池失败：%v", err)
		}
	})
	return db.DB
}

func checkSQLiteConnection(t *testing.T, ctx context.Context, conn *sql.Conn) {
	t.Helper()
	var timeout int
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("读取连接忙等待配置失败：%v", err)
	}
	if timeout != 5000 {
		t.Errorf("连接忙等待配置丢失：得到 %d，期望 5000", timeout)
	}
	var mode string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("读取连接日志模式失败：%v", err)
	}
	if mode != "wal" {
		t.Errorf("文件库没有使用 WAL：%s", mode)
	}
}

func TestSQLitePoolConfiguresEveryConnection(t *testing.T) {
	for _, name := range []string{"parallel_connections", "no_idle_connections", "replacement_connections", "uri_options"} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Default(t.TempDir()).Database
			cfg.DSN = filepath.Join(t.TempDir(), "pool.db")
			cfg.MaxOpenConns = 4
			cfg.MaxIdleConns = 4
			if name == "no_idle_connections" {
				cfg.MaxIdleConns = 0
			}
			if name == "uri_options" {
				cfg.DSN += "?_pragma=foreign_keys(1)&_pragma=busy_timeout(17)&_txlock=immediate"
			}
			db := openSQLiteTestPool(t, cfg)
			if name == "replacement_connections" {
				db.SetMaxIdleConns(0)
				db.SetMaxIdleConns(4)
			}
			// 同时持有所有连接，避免顺序查询反复使用初始化时的同一条连接。
			for i := 0; i < 4; i++ {
				conn, err := db.Conn(t.Context())
				if err != nil {
					t.Fatalf("取得第 %d 条连接失败：%v", i+1, err)
				}
				t.Cleanup(func() { _ = conn.Close() })
				checkSQLiteConnection(t, t.Context(), conn)
				if name == "uri_options" {
					var enabled int
					if err := conn.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
						t.Fatalf("用户提供的连接选项丢失：foreign_keys=%d，错误=%v", enabled, err)
					}
				}
			}
		})
	}
}
