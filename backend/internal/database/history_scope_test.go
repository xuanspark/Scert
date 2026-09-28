package database

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func seedHistoryScope(t *testing.T, db *sql.DB, driver string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), migrationTableSQL(driver)); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 12; version++ {
		if err := applyMigration(t.Context(), db, driver, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO domains (id, name, certificate_type, sans_json, status) VALUES ('domain', 'history.example.test', 'single', '[]', 'pending')`,
		`INSERT INTO servers (id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json, validation_command_json, ssh_key_error) VALUES ('server', '历史服务器', '192.0.2.1', 22, 'deploy', 'secret://test', 'test-only', '/test/cert.pem', '/test/key.pem', '/test/chain.pem', 'nginx', '[]', '[]', '')`,
		`INSERT INTO tasks (id, domain_id, task_type, status, message, operator_id) VALUES
			('business', 'domain', 'certificate_issue', 'success', '保留原记录', 'known-actor'),
			('orphan', 'deleted-domain', 'certificate_issue', 'success', '保留原记录', ''),
			('management', 'management-certificate', 'management_renewal', 'success', '保留原记录', ''),
			('unknown', 'management-certificate', 'certificate_issue', 'success', '保留原记录', '')`,
		`INSERT INTO task_attempts (id, task_id, attempt_number, stage, status, message) VALUES
			('business-attempt', 'business', 1, 'issue', 'success', '保留原记录'),
			('deleted-attempt', 'deleted-task', 1, 'issue', 'success', '保留原记录')`,
		`INSERT INTO audit_logs (action, resource_type, resource_id, status, message) VALUES
			('business', 'domain', 'domain', 'success', '保留原记录'),
			('certificate', 'certificate', 'domain', 'success', '保留原记录'),
			('server', 'server', 'server', 'success', '保留原记录'),
			('orphan', 'domain', 'deleted-domain', 'success', '保留原记录'),
			('task', 'task', 'business', 'success', '保留原记录'),
			('deleted-task', 'task', 'deleted-task', 'success', '保留原记录'),
			('management', 'management_certificate', 'management-certificate', 'success', '保留原记录'),
			('unknown', 'management_certificate', 'different', 'success', '保留原记录'),
			('auth', 'auth', 'login', 'success', '保留原记录')`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func assertHistoryScope(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT id, scope_type, project_id, operator_id, message FROM tasks ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		var id, scope, project, actor, message string
		if err := rows.Scan(&id, &scope, &project, &actor, &message); err != nil {
			t.Fatal(err)
		}
		wantScope, wantProject, wantActor := "unknown", "", ""
		if id == "business" {
			wantScope, wantProject, wantActor = "project", "default", "known-actor"
		}
		if id == "management" {
			wantScope = "system"
		}
		if scope != wantScope || project != wantProject || actor != wantActor || message != "保留原记录" {
			t.Errorf("任务历史错误：%s %s %s %s", id, scope, project, actor)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("任务历史丢失：%d", count)
	}
	rows, err = db.QueryContext(t.Context(), `SELECT action, scope_type, project_id, task_id, operator_id, message FROM audit_logs`)
	if err != nil {
		t.Fatal(err)
	}
	count = 0
	for rows.Next() {
		var action, scope, project, task, actor, message string
		if err := rows.Scan(&action, &scope, &project, &task, &actor, &message); err != nil {
			t.Fatal(err)
		}
		wantScope, wantProject, wantTask := "unknown", "", ""
		switch action {
		case "business", "certificate", "server", "task":
			wantScope, wantProject = "project", "default"
		case "management", "auth":
			wantScope = "system"
		}
		if action == "task" {
			wantTask = "business"
		}
		if action == "deleted-task" {
			wantTask = "deleted-task"
		}
		if scope != wantScope || project != wantProject || task != wantTask || actor != "" || message != "保留原记录" {
			t.Errorf("审计历史错误：%s %s %s %s %s", action, scope, project, task, actor)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 9 {
		t.Fatalf("审计历史丢失：%d", count)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM task_attempts WHERE message = '保留原记录'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("历史尝试被改写或删除：%d，%v", count, err)
	}
}

func TestHistoryScopeSQLiteMigration(t *testing.T) {
	db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedHistoryScope(t, db.DB, "sqlite")
	before := snapshotSQLiteHistoricalBusiness(t, db.DB)
	if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	assertSQLiteHistoricalBusiness(t, db.DB, before)
	assertHistoryScope(t, db.DB)
	after := snapshotSQLiteHistoricalBusiness(t, db.DB)
	if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	assertSQLiteHistoricalBusiness(t, db.DB, after)
}

func TestHistoryScopeSQLiteMigrationRollback(t *testing.T) {
	for _, tc := range []struct{ name, setup, repair string }{
		{"version", `CREATE TRIGGER fail_history_version BEFORE INSERT ON schema_migrations WHEN NEW.version = 13 BEGIN SELECT RAISE(ABORT, 'test-only'); END`, `DROP TRIGGER fail_history_version`},
		{"backfill", `CREATE TRIGGER fail_history_backfill BEFORE UPDATE ON audit_logs BEGIN SELECT RAISE(ABORT, 'test-only'); END`, `DROP TRIGGER fail_history_backfill`},
		{"index", `CREATE INDEX idx_audit_project_history ON audit_logs(status)`, `DROP INDEX idx_audit_project_history`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			seedHistoryScope(t, db.DB, "sqlite")
			if _, err := db.DB.ExecContext(t.Context(), tc.setup); err != nil {
				t.Fatal(err)
			}
			before := snapshotSQLiteHistoricalBusiness(t, db.DB)
			schemaQuery := `SELECT type, name, sql FROM sqlite_master ORDER BY name`
			schema := snapshotSQLiteUpgradeQuery(t, db.DB, schemaQuery)
			if err := Migrate(t.Context(), db.DB, "sqlite"); err == nil {
				t.Fatal("故障迁移未拒绝")
			}
			assertSQLiteHistoricalBusiness(t, db.DB, before)
			if snapshotSQLiteUpgradeQuery(t, db.DB, schemaQuery) != schema {
				t.Fatal("失败迁移没有完整回滚模式")
			}
			if version, err := currentMigrationVersion(t.Context(), db.DB); err != nil || version != 12 {
				t.Fatalf("失败迁移版本错误：%d，%v", version, err)
			}
			if _, err := db.DB.ExecContext(t.Context(), tc.repair); err != nil {
				t.Fatal(err)
			}
			for repeat := 0; repeat < 2; repeat++ {
				if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
					t.Fatal(err)
				}
			}
			assertHistoryScope(t, db.DB)
		})
	}
}

func TestHistoryScopeMySQLMigrationRetry(t *testing.T) {
	fixture := startRealMySQL(t)
	for _, phase := range []string{"backfill", "version"} {
		t.Run(phase, func(t *testing.T) {
			db := fixture.database(t)
			ctx := t.Context()
			seedHistoryScope(t, db.DB, "mysql")
			var schema string
			if err := db.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			schema = "`" + strings.ReplaceAll(schema, "`", "``") + "`"
			target := "BEFORE INSERT ON " + schema + ".schema_migrations"
			if phase == "backfill" {
				target = "BEFORE UPDATE ON " + schema + ".audit_logs"
			}
			if _, err := fixture.admin.ExecContext(ctx, `CREATE TRIGGER `+schema+`.fail_history `+target+` FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'test-only'`); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(ctx, db.DB, "mysql"); err == nil {
				t.Fatal("故障未阻断迁移")
			} else {
				var mysqlErr *mysql.MySQLError
				// 必须命中注入阶段，不能把索引损坏等其他错误误当作故障注入成功。
				if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1644 || mysqlErr.Message != "test-only" {
					t.Fatalf("迁移在注入阶段 %s 之前意外失败，保留服务器日志供诊断：%v", phase, err)
				}
			}
			if version, err := currentMigrationVersion(ctx, db.DB); err != nil || version != 12 {
				t.Fatalf("失败版本错误：%d，%v", version, err)
			}
			assertMySQLHistoryIndexes(t, db.DB)
			if _, err := fixture.admin.ExecContext(ctx, "DROP TRIGGER "+schema+".fail_history"); err != nil {
				t.Fatal(err)
			}
			for repeat := 0; repeat < 2; repeat++ {
				if err := Migrate(ctx, db.DB, "mysql"); err != nil {
					t.Fatal(err)
				}
				assertMySQLHistoryIndexes(t, db.DB)
			}
			assertHistoryScope(t, db.DB)
			if version, err := currentMigrationVersion(ctx, db.DB); err != nil || version != latestMigrationVersion {
				t.Fatalf("重试版本错误：%d，%v", version, err)
			}
		})
	}
}

func assertMySQLHistoryIndexes(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, index := range []struct{ table, name, columns string }{
		{"tasks", "idx_tasks_project_history", "project_id,created_at"},
		{"audit_logs", "idx_audit_project_history", "project_id,created_at"},
		{"audit_logs", "idx_audit_task_history", "task_id"},
	} {
		var columns sql.NullString
		err := db.QueryRowContext(t.Context(), `SELECT GROUP_CONCAT(column_name ORDER BY seq_in_index)
			FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=?`,
			index.table, index.name).Scan(&columns)
		if err != nil || !columns.Valid || columns.String != index.columns {
			t.Fatalf("历史范围索引结构不完整：%s.%s，字段=%s，错误=%v", index.table, index.name, columns.String, err)
		}
	}
}
