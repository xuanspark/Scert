package database

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMySQLRealTaskReportMigrationRecovery(t *testing.T) {
	f := startRealMySQL(t)
	baseline := f.database(t)
	if err := Migrate(t.Context(), baseline.DB, "mysql"); err != nil {
		t.Fatal(err)
	}
	expectedSchema := mysqlSchemaSnapshot(t, baseline.DB)
	content, err := json.Marshal(map[string]string{"message": strings.Repeat("部署报告", 20000)})
	if err != nil {
		t.Fatal(err)
	}
	for _, boundary := range []string{"before_create", "after_create", "with_report"} {
		t.Run(boundary, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 19)
			if boundary != "before_create" {
				mysqlDeploymentExec(t, db.DB, mysqlDeploymentStatements(t, 20)[0])
			}
			if boundary == "with_report" {
				if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO task_deployment_reports
					(task_id, attempt_id, attempt_number, report_json) VALUES ('report-task', 'report-attempt', 1, ?)`, string(content)); err != nil {
					t.Fatal(err)
				}
			}
			mysqlAssertUpgrade(t, db.DB, expectedSchema)
			if boundary == "with_report" {
				var stored string
				if err := db.DB.QueryRowContext(t.Context(), `SELECT report_json FROM task_deployment_reports
					WHERE task_id = 'report-task' AND attempt_id = 'report-attempt'`).Scan(&stored); err != nil || stored != string(content) {
					t.Fatalf("恢复迁移必须保留超过普通文本字段容量的中文报告：%v", err)
				}
			}
		})
	}
	for _, invalid := range []struct {
		name      string
		statement string
	}{
		{"short_report", "ALTER TABLE task_deployment_reports MODIFY COLUMN report_json TEXT NOT NULL"},
		{"wrong_primary_order", "ALTER TABLE task_deployment_reports DROP PRIMARY KEY, ADD PRIMARY KEY (attempt_id, task_id)"},
		{"nullable_attempt", "ALTER TABLE task_deployment_reports MODIFY COLUMN attempt_number INT NULL"},
		{"wrong_engine", "ALTER TABLE task_deployment_reports ENGINE=MyISAM"},
		{"extra_column", "ALTER TABLE task_deployment_reports ADD COLUMN unexpected INT NOT NULL DEFAULT 0"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 19)
			mysqlDeploymentExec(t, db.DB, mysqlDeploymentStatements(t, 20)[0])
			mysqlDeploymentExec(t, db.DB, `INSERT INTO task_deployment_reports
				(task_id, attempt_id, attempt_number, report_json) VALUES ('kept-task', 'kept-attempt', 1, '{"status":"failed"}')`)
			mysqlDeploymentExec(t, db.DB, invalid.statement)
			before := fmt.Sprint(mysqlSchemaSnapshot(t, db.DB))
			assertMySQLDeploymentMigrationRejected(t, db.DB, 19)
			if after := fmt.Sprint(mysqlSchemaSnapshot(t, db.DB)); after != before {
				t.Fatal("报告表冲突不得被自动覆盖或修复")
			}
			var stored string
			if err := db.DB.QueryRowContext(t.Context(), `SELECT report_json FROM task_deployment_reports
				WHERE task_id = 'kept-task' AND attempt_id = 'kept-attempt'`).Scan(&stored); err != nil || stored != `{"status":"failed"}` {
				t.Fatalf("拒绝迁移时不得修改已有报告：%v", err)
			}
		})
	}
}
