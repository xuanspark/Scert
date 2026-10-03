package database

import (
	"fmt"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/postgrestest"
)

func TestSQLiteDNSAccountProjectGuards(t *testing.T) {
	for _, foreignKeys := range []int{0, 1} {
		for _, recursive := range []int{0, 1} {
			t.Run(fmt.Sprintf("foreign_keys_%d_recursive_%d", foreignKeys, recursive), func(t *testing.T) {
				cfg := sqliteBusinessUpgradeConfig(t)
				cfg.MaxOpenConns, cfg.MaxIdleConns = 1, 1
				db, err := Open(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				for _, statement := range []string{fmt.Sprintf("PRAGMA foreign_keys=%d", foreignKeys), fmt.Sprintf("PRAGMA recursive_triggers=%d", recursive)} {
					if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
						t.Fatal(err)
					}
				}
				if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO projects (id,name,description) VALUES ('dns-only','账户项目','')`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO dns_accounts VALUES ('kept','账户','dns-only','retired-owner','aliyun','secret://dns/kept','active','unverified',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
					t.Fatal(err)
				}
				for _, statement := range []string{
					`INSERT INTO dns_accounts VALUES ('orphan','账户','missing','owner','aliyun','secret://dns/orphan','active','unverified',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
					`UPDATE dns_accounts SET project_id='missing' WHERE id='kept'`,
					`DELETE FROM projects WHERE id='dns-only'`,
					`UPDATE projects SET id='moved' WHERE id='dns-only'`,
					`INSERT OR REPLACE INTO projects (id,name,description) VALUES ('dns-only','覆盖','')`,
				} {
					if _, err := db.DB.ExecContext(t.Context(), statement); err == nil {
						t.Fatal("项目保护没有拒绝破坏引用的操作")
					}
				}
				for name, want := range map[string]int{"foreign_keys": foreignKeys, "recursive_triggers": recursive} {
					var value int
					if err := db.DB.QueryRowContext(t.Context(), "PRAGMA "+name).Scan(&value); err != nil || value != want {
						t.Fatal("迁移改变用户连接参数", name, value, err)
					}
				}
			})
		}
	}
}

func TestDNSAccountMigration(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var cfg config.DatabaseConfig
			switch driver {
			case "sqlite":
				cfg = sqliteBusinessUpgradeConfig(t)
			case "mysql":
				cfg = mysqltest.New(t).Database(t)
			case "postgres":
				cfg = postgrestest.New(t).Database(t)
			}
			db, err := Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.DB.ExecContext(t.Context(), migrationTableSQL(driver)); err != nil {
				t.Fatal(err)
			}
			for version := 1; version <= 26; version++ {
				if err := applyMigration(t.Context(), db.DB, driver, version); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO domains (id,name,certificate_type,sans_json,status,challenge_type)
				VALUES ('legacy-dns','legacy.example.com','single','[]','valid','http-01')`); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(t.Context(), db.DB, driver); err != nil {
				t.Fatal(err)
			}
			query := `INSERT INTO dns_accounts (id,name,project_id,owner_user_id,provider,credential_ref,status,verification_state,revision,created_at,updated_at)
				VALUES ('kept','保留账户','default','missing-owner','aliyun','secret://dns/kept','disabled','unverified',9,?,?)`
			if driver == "postgres" {
				query = `INSERT INTO dns_accounts (id,name,project_id,owner_user_id,provider,credential_ref,status,verification_state,revision,created_at,updated_at)
					VALUES ('kept','保留账户','default','missing-owner','aliyun','secret://dns/kept','disabled','unverified',9,$1,$2)`
			}
			if _, err := db.DB.ExecContext(t.Context(), query, time.Now().UTC(), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(t.Context(), db.DB, driver); err != nil {
				t.Fatal(err)
			}
			var status, challenge string
			if err := db.DB.QueryRowContext(t.Context(), `SELECT status,challenge_type FROM domains WHERE id='legacy-dns'`).Scan(&status, &challenge); err != nil || status != "valid" || challenge != "http-01" {
				t.Fatal("升级改变历史域名", err)
			}
			var revision int
			if err := db.DB.QueryRowContext(t.Context(), `SELECT status,revision FROM dns_accounts WHERE id='kept'`).Scan(&status, &revision); err != nil || status != "disabled" || revision != 9 {
				t.Fatal("重入覆盖已有账户", err)
			}
			if _, err := db.DB.ExecContext(t.Context(), `UPDATE dns_accounts SET project_id='missing' WHERE id='kept'`); err == nil {
				t.Fatal("项目外键没有生效")
			}
		})
	}
}

func TestMySQLDNSAccountInterruptedMigration(t *testing.T) {
	f := startRealMySQL(t)
	for _, boundary := range []string{"before_create", "after_create", "after_constraint", "with_record", "bad_column", "bad_index", "bad_foreign_key", "bad_collation"} {
		t.Run(boundary, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 26)
			statements := mysqlDeploymentStatements(t, 27)
			if boundary != "before_create" {
				mysqlDeploymentExec(t, db.DB, statements[0])
			}
			if boundary == "after_constraint" || boundary == "with_record" {
				mysqlDeploymentExec(t, db.DB, statements[1])
			}
			conflict := true
			switch boundary {
			case "bad_column":
				mysqlDeploymentExec(t, db.DB, "ALTER TABLE dns_accounts MODIFY COLUMN revision INT NULL")
			case "bad_index":
				mysqlDeploymentExec(t, db.DB, "ALTER TABLE dns_accounts DROP INDEX uq_dns_accounts_credential")
			case "bad_foreign_key":
				mysqlDeploymentExec(t, db.DB, "ALTER TABLE dns_accounts ADD CONSTRAINT fk_dns_accounts_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE")
			case "bad_collation":
				mysqlDeploymentExec(t, db.DB, "ALTER TABLE dns_accounts CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
			default:
				conflict = false
			}
			if boundary == "with_record" {
				mysqlDeploymentExec(t, db.DB, `INSERT INTO dns_accounts VALUES ('kept','保留账户','default','owner','aliyun','secret://dns/kept','disabled','unverified',7,NOW(),NOW())`)
			}
			err := Migrate(t.Context(), db.DB, "mysql")
			if (err != nil) != conflict {
				t.Fatal("中断恢复或冲突拒绝错误", err)
			}
			version, err := currentMigrationVersion(t.Context(), db.DB)
			if err != nil || (conflict && version != 26) || (!conflict && version != latestMigrationVersion) {
				t.Fatal("迁移版本错误", version, err)
			}
			if boundary == "with_record" {
				var revision int
				if err := db.DB.QueryRowContext(t.Context(), `SELECT revision FROM dns_accounts WHERE id='kept'`).Scan(&revision); err != nil || revision != 7 {
					t.Fatal("中断恢复丢失原有账户", err)
				}
			}
		})
	}
}
