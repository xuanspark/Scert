package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestOpenAndMigrateSQLite(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.Database.DSN = filepath.Join(dir, "data", "test.db")
	db, err := Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatalf("打开 SQLite 失败：%v", err)
	}
	defer db.Close()
	if err := Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatalf("SQLite 迁移失败：%v", err)
	}
	var count int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("读取迁移版本失败：%v", err)
	}
	if count != 3 {
		t.Fatalf("迁移版本数量错误：%d", count)
	}
	var autoRenew bool
	if err := db.DB.QueryRow("SELECT auto_renew FROM domains LIMIT 1").Scan(&autoRenew); err != nil && err != sql.ErrNoRows {
		t.Fatalf("检查域名选项字段失败：%v", err)
	}
}
