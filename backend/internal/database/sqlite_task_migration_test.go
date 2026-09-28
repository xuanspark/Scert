package database

import (
	"fmt"
	"strings"
	"testing"
)

func TestSQLiteBusinessUpgradePreservesDependentObjects(t *testing.T) {
	for _, foreignKeys := range []int{0, 1} {
		t.Run(fmt.Sprintf("foreign_keys_%d", foreignKeys), func(t *testing.T) {
			db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.DB.SetMaxOpenConns(1)
			createSQLiteHistoricalBusiness(t, db.DB, 3)
			for _, statement := range []string{
				fmt.Sprintf("PRAGMA foreign_keys = %d", foreignKeys),
				`ALTER TABLE tasks ADD COLUMN extra TEXT NOT NULL DEFAULT '扩展值)'`,
				`ALTER TABLE tasks ADD COLUMN generated TEXT GENERATED ALWAYS AS (message || extra) VIRTUAL`,
				`UPDATE tasks SET rowid = 77 WHERE id = 'task'`,
				`CREATE UNIQUE INDEX "历史索引" ON tasks(extra, id) WHERE status = 'success'`,
				`CREATE TABLE task_links (task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE)`,
				`INSERT INTO task_links VALUES ('task')`,
				`CREATE TABLE trigger_events (message TEXT)`,
				`CREATE TRIGGER "历史任务触发器" AFTER UPDATE ON tasks BEGIN INSERT INTO trigger_events VALUES (new.message); END`,
				`CREATE VIEW z_task_view AS SELECT id, message FROM tasks`,
				`CREATE VIEW a_task_view AS SELECT * FROM z_task_view`,
				`CREATE TRIGGER view_insert INSTEAD OF INSERT ON a_task_view BEGIN UPDATE tasks SET message = new.message WHERE id = new.id; END`,
				`CREATE TRIGGER external_insert AFTER INSERT ON settings BEGIN UPDATE tasks SET message = new.value WHERE id = 'pending'; END`,
			} {
				if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotSQLiteHistoricalBusiness(t, db.DB)
			objectsQuery := `SELECT type, name, sql FROM sqlite_master WHERE name IN ('历史索引', '历史任务触发器', 'z_task_view', 'a_task_view', 'view_insert', 'external_insert', 'task_links') ORDER BY name`
			objects := snapshotSQLiteUpgradeQuery(t, db.DB, objectsQuery)
			generated := snapshotSQLiteUpgradeQuery(t, db.DB, "SELECT rowid, id, generated FROM tasks ORDER BY id")
			if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
				t.Fatal(err)
			}
			assertSQLiteHistoricalBusiness(t, db.DB, before)
			if actual := snapshotSQLiteUpgradeQuery(t, db.DB, objectsQuery); actual != objects {
				t.Fatal("依赖对象定义未完整保留")
			}
			if actual := snapshotSQLiteUpgradeQuery(t, db.DB, "SELECT rowid, id, generated FROM tasks ORDER BY id"); actual != generated {
				t.Fatal("行号或生成列发生变化")
			}
			var count, actualForeignKeys int
			if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM trigger_events").Scan(&count); err != nil || count != 2 {
				t.Fatalf("范围回填应保留并调用两次历史更新触发器：%d，%v", count, err)
			}
			if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM task_links WHERE task_id = 'task'").Scan(&count); err != nil || count != 1 {
				t.Fatalf("任务表重建触发了级联删除：%v", err)
			}
			if err := db.DB.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&actualForeignKeys); err != nil || actualForeignKeys != foreignKeys {
				t.Fatalf("连接的外键设置未恢复：%d，%v", actualForeignKeys, err)
			}
			if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO a_task_view VALUES ('task', '升级后视图写入'); INSERT INTO settings (key, value) VALUES ('after', '升级后外部触发器')`); err != nil {
				t.Fatal(err)
			}
			if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM trigger_events").Scan(&count); err != nil || count != 4 {
				t.Fatalf("触发器执行结果错误：%d，%v", count, err)
			}
		})
	}
}

func TestSQLiteBusinessUpgradeFailureRollsBackAndRetries(t *testing.T) {
	cases := []struct {
		name    string
		prepare string
		cleanup string
		message string
	}{
		{"version_record", `CREATE TRIGGER reject_version BEFORE INSERT ON schema_migrations WHEN new.version = 4 BEGIN SELECT RAISE(ABORT, 'blocked-upgrade'); END`, "DROP TRIGGER reject_version", "blocked-upgrade"},
		{"temporary_name", `CREATE TABLE scert_tasks_migration_4 (value TEXT); INSERT INTO scert_tasks_migration_4 VALUES ('保留内容')`, "DROP TABLE scert_tasks_migration_4", "already exists"},
		{"foreign_key", `CREATE TABLE broken_links (id TEXT REFERENCES tasks(id)); INSERT INTO broken_links VALUES ('missing-task')`, "DELETE FROM broken_links", "外键冲突"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.DB.SetMaxOpenConns(1)
			createSQLiteHistoricalBusiness(t, db.DB, 3)
			if _, err := db.DB.ExecContext(t.Context(), tc.prepare); err != nil {
				t.Fatal(err)
			}
			if _, err := db.DB.ExecContext(t.Context(), "PRAGMA foreign_keys = ON"); err != nil {
				t.Fatal(err)
			}
			before := snapshotSQLiteHistoricalBusiness(t, db.DB)
			schemaQuery := "SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY type, name"
			schema := snapshotSQLiteUpgradeQuery(t, db.DB, schemaQuery)
			history := snapshotSQLiteUpgradeQuery(t, db.DB, "SELECT * FROM schema_migrations ORDER BY version")
			if err := Migrate(t.Context(), db.DB, "sqlite"); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("未拒绝有阻塞项的迁移：%v", err)
			}
			assertSQLiteHistoricalBusiness(t, db.DB, before)
			if actual := snapshotSQLiteUpgradeQuery(t, db.DB, schemaQuery); actual != schema {
				t.Fatal("迁移失败后表、索引或触发器定义被改变")
			}
			if actual := snapshotSQLiteUpgradeQuery(t, db.DB, "SELECT * FROM schema_migrations ORDER BY version"); actual != history {
				t.Fatal("迁移失败后版本被错误推进")
			}
			var foreignKeys int
			if err := db.DB.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
				t.Fatalf("失败路径未恢复外键设置：%d，%v", foreignKeys, err)
			}
			if _, err := db.DB.ExecContext(t.Context(), tc.cleanup); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
				t.Fatalf("排除阻塞后无法重新升级：%v", err)
			}
			assertSQLiteHistoricalBusiness(t, db.DB, before)
		})
	}
}
