package database

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestMySQLDeploymentShapeDefinitions(t *testing.T) {
	for _, version := range []int{15, 16, 17, 20, 21, 22, 23, 24} {
		statements := mysqlDeploymentStatements(t, version)
		shape, err := parseMySQLDeploymentShape(statements[0])
		if err != nil || len(shape.Columns) == 0 || len(shape.Indexes) == 0 {
			t.Fatalf("内嵌迁移定义必须完整解析：%d：%v", version, err)
		}
		for _, invalid := range []string{
			strings.Replace(statements[0], "NOT NULL", "NULL", 1),
			strings.Replace(statements[0], "VARCHAR", "CHAR", 1),
			strings.Replace(statements[0], "CREATE TABLE", "CREATE VIEW", 1),
		} {
			if _, err := parseMySQLDeploymentShape(invalid); err == nil {
				t.Fatal("未知迁移定义不能被静默忽略")
			}
		}
	}
}

func TestMySQLRealDeploymentMigrationRecovery(t *testing.T) {
	f := startRealMySQL(t)
	baseline := f.database(t)
	if err := Migrate(t.Context(), baseline.DB, "mysql"); err != nil {
		t.Fatal(err)
	}
	expectedSchema := mysqlSchemaSnapshot(t, baseline.DB)
	for _, boundary := range []string{"before_create", "after_create", "partial_backfill", "after_backfill"} {
		t.Run(boundary, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 14)
			seedMySQLIdempotencyTasks(t, db.DB)
			statements := mysqlDeploymentStatements(t, 15)
			if boundary != "before_create" {
				mysqlDeploymentExec(t, db.DB, statements[0])
			}
			switch boundary {
			case "partial_backfill":
				mysqlDeploymentExec(t, db.DB, `INSERT INTO task_idempotency VALUES ('domain-a','request-a','task-a','2026-01-01 00:00:00')`)
			case "after_backfill":
				mysqlDeploymentExec(t, db.DB, statements[1])
			}
			mysqlAssertUpgrade(t, db.DB, expectedSchema)
			var count int
			if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM task_idempotency").Scan(&count); err != nil || count != 3 {
				t.Fatalf("幂等回填必须去重、跨域隔离并排除空键：%d：%v", count, err)
			}
			var task, created string
			if err := db.DB.QueryRowContext(t.Context(), `SELECT task_id, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') FROM task_idempotency WHERE domain_id='domain-a' AND idempotency_key='request-a'`).Scan(&task, &created); err != nil || task != "task-a" || created != "2026-01-01 00:00:00" {
				t.Fatalf("历史代表任务及最早创建时间必须保留：%s：%s：%v", task, created, err)
			}
		})
	}
	for _, invalid := range []struct {
		name      string
		statement string
	}{
		{"wrong_task", `INSERT INTO task_idempotency VALUES ('domain-a','request-a','task-z','2026-01-01 00:00:00')`},
		{"wrong_task_case", `INSERT INTO task_idempotency VALUES ('domain-a','request-a','TASK-A','2026-01-01 00:00:00')`},
		{"wrong_time", `INSERT INTO task_idempotency VALUES ('domain-a','request-a','task-a','2026-01-02 00:00:00')`},
		{"orphan_mapping", `INSERT INTO task_idempotency VALUES ('unknown','unknown','unknown','2026-01-01 00:00:00')`},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 14)
			seedMySQLIdempotencyTasks(t, db.DB)
			mysqlDeploymentExec(t, db.DB, mysqlDeploymentStatements(t, 15)[0])
			mysqlDeploymentExec(t, db.DB, invalid.statement)
			var before string
			query := `SELECT CONCAT(domain_id, ':', idempotency_key, ':', task_id, ':', created_at) FROM task_idempotency`
			if err := db.DB.QueryRowContext(t.Context(), query).Scan(&before); err != nil {
				t.Fatal(err)
			}
			assertMySQLDeploymentMigrationRejected(t, db.DB, 14)
			var after string
			if err := db.DB.QueryRowContext(t.Context(), query).Scan(&after); err != nil || after != before {
				t.Fatalf("拒绝恢复不得修改冲突数据：%v", err)
			}
		})
	}
	for _, invalid := range []struct {
		name      string
		version   int
		statement string
	}{
		{"wrong_column", 15, "ALTER TABLE task_idempotency MODIFY COLUMN task_id VARCHAR(64) NOT NULL"},
		{"empty_string_default", 15, "ALTER TABLE task_idempotency ALTER COLUMN task_id SET DEFAULT ''"},
		{"wrong_collation", 15, "ALTER TABLE task_idempotency MODIFY COLUMN task_id VARCHAR(36) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL"},
		{"nullable_column", 15, "ALTER TABLE task_idempotency MODIFY COLUMN task_id VARCHAR(36) NULL"},
		{"missing_unique", 15, "ALTER TABLE task_idempotency DROP INDEX uq_task_idempotency_task"},
		{"wrong_default", 16, "ALTER TABLE task_deployment_results ALTER COLUMN attempt_number SET DEFAULT 7"},
		{"missing_index", 16, "ALTER TABLE task_deployment_results DROP INDEX idx_task_deployment_results_task_id"},
		{"wrong_engine", 17, "ALTER TABLE deployment_transactions ENGINE=MyISAM"},
		{"extra_column", 17, "ALTER TABLE deployment_transactions ADD COLUMN unexpected INT NOT NULL DEFAULT 0"},
		{"wrong_index_order", 17, "ALTER TABLE deployment_transactions DROP INDEX idx_deployment_transactions_status, ADD INDEX idx_deployment_transactions_status (updated_at, status)"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, invalid.version-1)
			mysqlDeploymentExec(t, db.DB, mysqlDeploymentStatements(t, invalid.version)[0])
			mysqlDeploymentExec(t, db.DB, invalid.statement)
			before := fmt.Sprint(mysqlSchemaSnapshot(t, db.DB))
			assertMySQLDeploymentMigrationRejected(t, db.DB, invalid.version-1)
			if after := fmt.Sprint(mysqlSchemaSnapshot(t, db.DB)); after != before {
				t.Fatal("拒绝恢复不得擅自修复或覆盖已有结构")
			}
		})
	}
}

func mysqlDeploymentStatements(t *testing.T, version int) []string {
	t.Helper()
	content, err := migrationFiles.ReadFile(fmt.Sprintf("migrations/mysql/%04d_%s.sql", version, migrationName(version)))
	if err != nil {
		t.Fatal(err)
	}
	return migrationStatements(content)
}

func mysqlDeploymentExec(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), statement); err != nil {
		t.Fatal(err)
	}
}

func seedMySQLIdempotencyTasks(t *testing.T, db *sql.DB) {
	t.Helper()
	mysqlDeploymentExec(t, db, `INSERT INTO tasks (id, domain_id, task_type, status, message, idempotency_key, created_at) VALUES
		('task-z','domain-a','deploy','failed','','request-a','2026-01-01 00:00:00'),
		('task-a','domain-a','deploy','success','','request-a','2026-01-02 00:00:00'),
		('task-b','domain-a','deploy','success','','request-b','2026-01-03 00:00:00'),
		('task-c','domain-b','deploy','success','','request-a','2026-01-04 00:00:00'),
		('task-empty','domain-a','deploy','success','','','2026-01-05 00:00:00')`)
}

func assertMySQLDeploymentMigrationRejected(t *testing.T, db *sql.DB, expectedVersion int) {
	t.Helper()
	for repeat := 0; repeat < 2; repeat++ {
		if err := Migrate(t.Context(), db, "mysql"); err == nil {
			t.Fatal("结构或历史冲突时必须拒绝迁移")
		}
		version, err := currentMigrationVersion(t.Context(), db)
		if err != nil || version != expectedVersion {
			t.Fatalf("拒绝恢复不得提前写入版本：%d：%v", version, err)
		}
		mysqlAssertPreserved(t, db)
	}
}
