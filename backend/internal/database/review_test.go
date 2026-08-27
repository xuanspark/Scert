package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestOpenRejectsUnknownDriver(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.Driver = "postgres"
	cfg.DSN = "unused"
	if _, err := Open(context.Background(), cfg); err == nil {
		t.Fatal("未知数据库驱动未被拒绝")
	}
}

func TestNormalizeMySQLDSNEnablesTimeParsing(t *testing.T) {
	dsn, err := normalizeMySQLDSN("user:password@tcp(127.0.0.1:3306)/scert?charset=utf8mb4")
	if err != nil {
		t.Fatalf("规范化 MySQL DSN 失败：%v", err)
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("解析规范化后的 MySQL DSN 失败：%v", err)
	}
	if !parsed.ParseTime || parsed.Loc != time.UTC {
		t.Fatal("MySQL DSN 未启用时间解析或未使用 UTC")
	}
}

func TestSQLiteMigrationIsIdempotentAndCreatesBaseTables(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir).Database
	cfg.DSN = filepath.Join(dir, "data", "review.db")
	db, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("打开 SQLite 失败：%v", err)
	}
	defer db.Close()
	for i := 0; i < 2; i++ {
		if err := Migrate(context.Background(), db.DB, "sqlite"); err != nil {
			t.Fatalf("第 %d 次迁移失败：%v", i+1, err)
		}
	}
	var versions int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&versions); err != nil {
		t.Fatalf("读取迁移版本失败：%v", err)
	}
	if versions != 3 {
		t.Fatalf("重复迁移写入了多条版本记录：%d", versions)
	}
	var tables int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('settings', 'domains', 'tasks', 'audit_logs', 'servers', 'domain_servers')`).Scan(&tables); err != nil {
		t.Fatalf("检查基础数据表失败：%v", err)
	}
	if tables != 6 {
		t.Fatalf("基础数据表数量不正确：%d", tables)
	}
	if err := Migrate(context.Background(), db.DB, "postgres"); err == nil {
		t.Fatal("未知迁移驱动未被拒绝")
	}
}
