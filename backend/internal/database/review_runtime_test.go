package database

import (
	"context"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestDatabasePingAndSQLitePragmas(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	db, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("打开 SQLite 失败：%v", err)
	}
	defer db.Close()
	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("数据库健康检查失败：%v", err)
	}
	var timeout int
	if err := db.DB.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("读取 SQLite 忙等待失败：%v", err)
	}
	if timeout != 5000 {
		t.Fatalf("SQLite 忙等待不正确：%d", timeout)
	}
	var mode string
	if err := db.DB.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("读取 SQLite 日志模式失败：%v", err)
	}
	if mode != "memory" && mode != "wal" {
		t.Fatalf("SQLite 日志模式不正确：%s", mode)
	}
}

func TestSanitizeDSNRedactsMySQLPassword(t *testing.T) {
	sanitized := SanitizeDSN("mysql", "user:secret@tcp(127.0.0.1:3306)/scert?charset=utf8mb4")
	if sanitized == "" || strings.Contains(sanitized, "secret") {
		t.Fatalf("MySQL DSN 未正确脱敏：%s", sanitized)
	}
}
