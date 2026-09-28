package database

import (
	"strings"
	"testing"
)

func TestMySQLRealProjectMigrationRetry(t *testing.T) {
	fixture := startRealMySQL(t)
	db := fixture.database(t)
	ctx := t.Context()
	if _, err := db.DB.ExecContext(ctx, migrationTableSQL("mysql")); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 9; version++ {
		if err := applyMigration(ctx, db.DB, "mysql", version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO tasks (id, domain_id, task_type, status, message)
		VALUES ('historical-task', 'historical-domain', 'deploy', 'success', '保留历史任务')`); err != nil {
		t.Fatal(err)
	}
	// MySQL 的 DDL 不会随事务回滚，必须验证建表后版本登记失败可安全重试。
	var schema string
	if err := db.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	schema = "`" + strings.ReplaceAll(schema, "`", "``") + "`"
	// 故障注入仅使用隔离实例管理连接，不给产品连接增加全局权限。
	if _, err := fixture.admin.ExecContext(ctx, `CREATE TRIGGER `+schema+`.fail_project_version BEFORE INSERT ON `+schema+`.schema_migrations
		FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'test-only'`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db.DB, "mysql"); err == nil {
		t.Fatal("版本登记故障未阻断 MySQL 迁移")
	}
	version, err := currentMigrationVersion(ctx, db.DB)
	if err != nil || version != 9 {
		t.Fatalf("失败迁移错误登记版本：%d，%v", version, err)
	}
	var name string
	if err := db.DB.QueryRowContext(ctx, "SELECT name FROM projects WHERE id = 'default'").Scan(&name); err != nil || name != "默认项目" {
		t.Fatalf("MySQL 部分迁移的默认项目缺失：%q，%v", name, err)
	}
	if _, err := db.DB.ExecContext(ctx, "UPDATE projects SET name = '保留用户调整' WHERE id = 'default'"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.admin.ExecContext(ctx, "DROP TRIGGER "+schema+".fail_project_version"); err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		if err := Migrate(ctx, db.DB, "mysql"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&count); err != nil || count != 1 {
		t.Fatalf("重试后项目数量错误：%d，%v", count, err)
	}
	if err := db.DB.QueryRowContext(ctx, "SELECT name FROM projects WHERE id = 'default'").Scan(&name); err != nil || name != "保留用户调整" {
		t.Fatalf("重试覆盖已有默认项目：%q，%v", name, err)
	}
	var message string
	if err := db.DB.QueryRowContext(ctx, "SELECT message FROM tasks WHERE id = 'historical-task'").Scan(&message); err != nil || message != "保留历史任务" {
		t.Fatalf("迁移影响历史任务：%q，%v", message, err)
	}
	version, err = currentMigrationVersion(ctx, db.DB)
	if err != nil || version != latestMigrationVersion {
		t.Fatalf("重试后迁移版本不完整：%d，%v", version, err)
	}
}
