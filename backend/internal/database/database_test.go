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
	if count != latestMigrationVersion {
		t.Fatalf("迁移版本数量错误：%d", count)
	}
	var autoRenew bool
	if err := db.DB.QueryRow("SELECT auto_renew FROM domains LIMIT 1").Scan(&autoRenew); err != nil && err != sql.ErrNoRows {
		t.Fatalf("检查域名选项字段失败：%v", err)
	}
}

func TestMySQLMigrationStatementsRecognizeRecoverableChanges(t *testing.T) {
	cases := []struct {
		statement string
		table     string
		name      string
	}{
		{statement: "ALTER TABLE domains ADD COLUMN auto_renew BOOLEAN NOT NULL DEFAULT TRUE", table: "domains", name: "auto_renew"},
		{statement: "ALTER TABLE `tasks` ADD COLUMN `stage` VARCHAR(32) NOT NULL DEFAULT 'queued'", table: "tasks", name: "stage"},
		{statement: "CREATE INDEX idx_tasks_status ON tasks(status)", table: "tasks", name: "idx_tasks_status"},
		{statement: "CREATE INDEX `idx_tasks_idempotency` ON `tasks`(idempotency_key)", table: "tasks", name: "idx_tasks_idempotency"},
	}
	for _, item := range cases {
		if matches := mysqlAddColumnPattern.FindStringSubmatch(item.statement); len(matches) == 3 {
			if matches[1] != item.table || matches[2] != item.name {
				t.Errorf("字段迁移解析结果不正确：statement=%q matches=%v", item.statement, matches)
			}
			continue
		}
		if matches := mysqlCreateIndexPattern.FindStringSubmatch(item.statement); len(matches) == 3 {
			if matches[1] != item.name || matches[2] != item.table {
				t.Errorf("索引迁移解析结果不正确：statement=%q matches=%v", item.statement, matches)
			}
			continue
		}
		t.Errorf("可恢复的 MySQL 迁移语句未被识别：%q", item.statement)
	}
}
