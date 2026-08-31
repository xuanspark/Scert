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
func TestSQLiteUpgradeAddsServerValidationCommand(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir).Database
	cfg.DSN = filepath.Join(dir, "data", "upgrade.db")
	db, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("打开升级测试数据库失败：%v", err)
	}
	defer db.Close()
	if _, err := db.DB.ExecContext(t.Context(), migrationTableSQL("sqlite")); err != nil {
		t.Fatalf("创建迁移表失败：%v", err)
	}
	for version := 1; version <= 5; version++ {
		if err := applyMigration(t.Context(), db.DB, "sqlite", version); err != nil {
			t.Fatalf("应用第 %d 版迁移失败：%v", version, err)
		}
	}
	_, err = db.DB.ExecContext(t.Context(), `INSERT INTO servers
		(id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"server-upgrade", "升级测试服务器", "192.0.2.80", 22, "deploy", "secret://ssh/upgrade", "SHA256:test",
		"/ssl/cert.pem", "/ssl/key.pem", "/ssl/fullchain.pem", "nginx", `["nginx","-s","reload"]`)
	if err != nil {
		t.Fatalf("写入旧版服务器记录失败：%v", err)
	}
	if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatalf("升级到最新数据库版本失败：%v", err)
	}
	var validationCommand string
	if err := db.DB.QueryRowContext(t.Context(), "SELECT validation_command_json FROM servers WHERE id = ?", "server-upgrade").Scan(&validationCommand); err != nil {
		t.Fatalf("读取升级后的配置校验命令失败：%v", err)
	}
	if validationCommand != "[]" {
		t.Fatalf("旧服务器记录的配置校验命令默认值错误：%s", validationCommand)
	}
}
