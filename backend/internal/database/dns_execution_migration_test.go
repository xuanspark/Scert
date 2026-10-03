package database

import "testing"

func TestMySQLDNSExecutionInterruptedMigration(t *testing.T) {
	f := startRealMySQL(t)
	for _, boundary := range []string{"before-create", "after-create", "with-record", "bad-column", "bad-index", "bad-collation"} {
		t.Run(boundary, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 28)
			statements := mysqlDeploymentStatements(t, 29)
			if boundary != "before-create" {
				mysqlDeploymentExec(t, db.DB, statements[0])
			}
			conflict := true
			switch boundary {
			case "bad-column":
				mysqlDeploymentExec(t, db.DB, "ALTER TABLE dns_execution_leases MODIFY COLUMN account_revision INT NULL")
			case "bad-index":
				mysqlDeploymentExec(t, db.DB, "ALTER TABLE dns_execution_leases DROP INDEX uq_dns_leases_attempt")
			case "bad-collation":
				mysqlDeploymentExec(t, db.DB, "ALTER TABLE dns_execution_leases CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
			default:
				conflict = false
			}
			if boundary == "with-record" {
				mysqlDeploymentExec(t, db.DB, `INSERT INTO dns_execution_leases VALUES ('kept','domain','binding','project','account',9,'cloudflare','secret://dns/test','task','attempt',1,0,'active',NOW(),NOW())`)
			}
			err := Migrate(t.Context(), db.DB, "mysql")
			if (err != nil) != conflict {
				t.Fatal("租约迁移中断恢复或冲突拒绝失败", err)
			}
			version, err := currentMigrationVersion(t.Context(), db.DB)
			if err != nil || (conflict && version != 28) || (!conflict && version != latestMigrationVersion) {
				t.Fatal("错误标记迁移版本", version, err)
			}
			if boundary == "with-record" {
				var status string
				if err := db.DB.QueryRowContext(t.Context(), `SELECT status FROM dns_execution_leases WHERE id = 'kept'`).Scan(&status); err != nil || status != "active" {
					t.Fatal("重入丢失活动秘密引用", err)
				}
			}
		})
	}
}
