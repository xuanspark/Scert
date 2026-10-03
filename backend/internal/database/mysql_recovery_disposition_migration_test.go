package database

import (
	"fmt"
	"testing"
)

func TestMySQLRealRecoveryDispositionMigrationRecovery(t *testing.T) {
	f := startRealMySQL(t)
	baseline := f.database(t)
	if err := Migrate(t.Context(), baseline.DB, "mysql"); err != nil {
		t.Fatal(err)
	}
	expected := mysqlSchemaSnapshot(t, baseline.DB)
	for _, item := range []struct {
		version  int
		table    string
		seed     string
		conflict string
	}{
		{22, "recovery_dispositions", `INSERT INTO recovery_dispositions VALUES
			('kept', 'transaction', 'server', 'unix:0', 'record_inspection', 'request', 'record', 'server', 'observation', 'running', '', NOW(), NOW())`,
			`ALTER TABLE recovery_dispositions DROP INDEX uq_recovery_request`},
		{23, "recovery_claims", `INSERT INTO recovery_claims VALUES ('transaction', 'kept', NOW())`,
			`ALTER TABLE recovery_claims DROP PRIMARY KEY`},
		{24, "recovery_events", `INSERT INTO recovery_events VALUES ('kept', 'started', '', NOW())`,
			`ALTER TABLE recovery_events DROP PRIMARY KEY, ADD PRIMARY KEY (event_type, disposition_id)`},
		{25, "recovery_dispositions", `INSERT INTO recovery_dispositions (id, transaction_id, server_id, operator_id, action, request_key,
			record_digest, server_digest, observation_digest, status, result_code, created_at, updated_at) VALUES
			('kept', 'transaction', 'server', 'unix:0', 'finish_commit', 'request', 'record', 'server', 'observation', 'uncertain', 'uncertain', NOW(), NOW())`,
			`ALTER TABLE recovery_dispositions MODIFY COLUMN source_disposition_id VARCHAR(8) NOT NULL DEFAULT ''`},
	} {
		for _, boundary := range []string{"before_create", "after_create", "with_record", "conflict"} {
			t.Run(fmt.Sprintf("%d/%s", item.version, boundary), func(t *testing.T) {
				db := f.database(t)
				mysqlPrepareVersion(t, db.DB, item.version-1)
				if boundary != "before_create" {
					mysqlDeploymentExec(t, db.DB, mysqlDeploymentStatements(t, item.version)[0])
				}
				if boundary == "with_record" || boundary == "conflict" {
					mysqlDeploymentExec(t, db.DB, item.seed)
				}
				if boundary == "conflict" {
					mysqlDeploymentExec(t, db.DB, item.conflict)
					before := fmt.Sprint(mysqlSchemaSnapshot(t, db.DB))
					assertMySQLDeploymentMigrationRejected(t, db.DB, item.version-1)
					if after := fmt.Sprint(mysqlSchemaSnapshot(t, db.DB)); after != before {
						t.Fatal("结构冲突不得自动修复或覆盖")
					}
				} else {
					mysqlAssertUpgrade(t, db.DB, expected)
				}
				if boundary == "with_record" || boundary == "conflict" {
					var count int
					if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+item.table).Scan(&count); err != nil || count != 1 {
						t.Fatal("迁移或冲突拒绝丢失已有处置记录", err)
					}
				}
			})
		}
	}
}
