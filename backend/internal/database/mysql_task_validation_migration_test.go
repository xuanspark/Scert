package database

import (
	"fmt"
	"testing"
)

func TestMySQLRealTaskValidationMigrationRecovery(t *testing.T) {
	f := startRealMySQL(t)
	baseline := f.database(t)
	if err := Migrate(t.Context(), baseline.DB, "mysql"); err != nil {
		t.Fatal(err)
	}
	expectedSchema := mysqlSchemaSnapshot(t, baseline.DB)
	const validation = `{"domains":["example.test"],"fingerprint_sha256":"retained-validation"}`
	for _, boundary := range []string{"before_create", "after_create", "with_validation"} {
		t.Run(boundary, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 20)
			if boundary != "before_create" {
				mysqlDeploymentExec(t, db.DB, mysqlDeploymentStatements(t, 21)[0])
			}
			if boundary == "with_validation" {
				if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO task_validation_results
					(task_id, attempt_id, attempt_number, validation_json) VALUES ('validation-task', 'validation-attempt', 1, ?)`, validation); err != nil {
					t.Fatal(err)
				}
			}
			mysqlAssertUpgrade(t, db.DB, expectedSchema)
			if boundary == "with_validation" {
				var stored string
				if err := db.DB.QueryRowContext(t.Context(), `SELECT validation_json FROM task_validation_results
					WHERE task_id = 'validation-task' AND attempt_id = 'validation-attempt'`).Scan(&stored); err != nil || stored != validation {
					t.Fatalf("恢复迁移后校验详情被改写：%v", err)
				}
			}
		})
	}
	for _, invalid := range []struct{ name, statement string }{
		{"short_result", "ALTER TABLE task_validation_results MODIFY COLUMN validation_json TEXT NOT NULL"},
		{"wrong_primary_order", "ALTER TABLE task_validation_results DROP PRIMARY KEY, ADD PRIMARY KEY (attempt_id, task_id)"},
		{"nullable_attempt", "ALTER TABLE task_validation_results MODIFY COLUMN attempt_number INT NULL"},
		{"wrong_engine", "ALTER TABLE task_validation_results ENGINE=MyISAM"},
		{"extra_column", "ALTER TABLE task_validation_results ADD COLUMN unexpected INT NOT NULL DEFAULT 0"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 20)
			mysqlDeploymentExec(t, db.DB, mysqlDeploymentStatements(t, 21)[0])
			if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO task_validation_results
				(task_id, attempt_id, attempt_number, validation_json) VALUES ('kept-task', 'kept-attempt', 1, ?)`, validation); err != nil {
				t.Fatal(err)
			}
			mysqlDeploymentExec(t, db.DB, invalid.statement)
			before := fmt.Sprint(mysqlSchemaSnapshot(t, db.DB))
			assertMySQLDeploymentMigrationRejected(t, db.DB, 20)
			if after := fmt.Sprint(mysqlSchemaSnapshot(t, db.DB)); after != before {
				t.Fatal("校验结果表冲突不得被自动覆盖或修复")
			}
			var stored string
			if err := db.DB.QueryRowContext(t.Context(), `SELECT validation_json FROM task_validation_results
				WHERE task_id = 'kept-task' AND attempt_id = 'kept-attempt'`).Scan(&stored); err != nil || stored != validation {
				t.Fatalf("拒绝迁移时不得修改已有校验结果：%v", err)
			}
		})
	}
}
