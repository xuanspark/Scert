package database

import (
	"testing"
)

func TestSQLiteProjectMigrationPreservesBusiness(t *testing.T) {
	db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createSQLiteHistoricalBusiness(t, db.DB, 9)
	before := snapshotSQLiteHistoricalBusiness(t, db.DB)
	if err := applyMigration(t.Context(), db.DB, "sqlite", 10); err != nil {
		t.Fatal(err)
	}
	assertSQLiteHistoricalBusiness(t, db.DB, before)
	var name string
	if err := db.DB.QueryRowContext(t.Context(), "SELECT name FROM projects WHERE id = 'default'").Scan(&name); err != nil || name != "默认项目" {
		t.Fatalf("默认项目未创建：%q，%v", name, err)
	}
	if _, err := db.DB.ExecContext(t.Context(), "UPDATE projects SET name = '调整后的默认项目' WHERE id = 'default'"); err != nil {
		t.Fatal(err)
	}
	// 模拟建表、种子写入已成功，但版本记录尚未落盘的可重试情况。
	if _, err := db.DB.ExecContext(t.Context(), "DELETE FROM schema_migrations WHERE version = 10"); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM projects").Scan(&count); err != nil || count != 1 {
		t.Fatalf("重复迁移改变项目数量：%d，%v", count, err)
	}
	if err := db.DB.QueryRowContext(t.Context(), "SELECT name FROM projects WHERE id = 'default'").Scan(&name); err != nil || name != "调整后的默认项目" {
		t.Fatalf("重复迁移覆盖用户修改：%q，%v", name, err)
	}
	assertSQLiteHistoricalBusiness(t, db.DB, before)
}

func TestSQLiteProjectMigrationRollsBackAndRetries(t *testing.T) {
	db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createSQLiteHistoricalBusiness(t, db.DB, 9)
	before := snapshotSQLiteHistoricalBusiness(t, db.DB)
	// 在登记版本时制造失败，验证同一事务中的新表及默认项目也会回滚。
	_, err = db.DB.ExecContext(t.Context(), `CREATE TRIGGER fail_project_version BEFORE INSERT ON schema_migrations
		WHEN NEW.version = 10 BEGIN SELECT RAISE(ABORT, 'test-only'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), db.DB, "sqlite"); err == nil {
		t.Fatal("版本登记故障未阻断迁移")
	}
	version, err := currentMigrationVersion(t.Context(), db.DB)
	if err != nil || version != 9 {
		t.Fatalf("失败迁移错误登记版本：%d，%v", version, err)
	}
	var count int
	if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE name = 'projects'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("失败迁移未回滚项目表：%d，%v", count, err)
	}
	assertSQLiteHistoricalBusiness(t, db.DB, before)
	if _, err := db.DB.ExecContext(t.Context(), "DROP TRIGGER fail_project_version"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	assertSQLiteHistoricalBusiness(t, db.DB, before)
}
