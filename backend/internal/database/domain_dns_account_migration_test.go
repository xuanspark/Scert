package database

import (
	"database/sql"
	"fmt"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/postgrestest"
)

func TestDomainDNSAccountMigration(t *testing.T) {
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
			for version := 1; version <= 27; version++ {
				if err := applyMigration(t.Context(), db.DB, driver, version); err != nil {
					t.Fatal(err)
				}
			}
			seedDomainDNSMigration(t, db.DB)
			if err := Migrate(t.Context(), db.DB, driver); err != nil {
				t.Fatal(err)
			}
			checkDomainDNSConstraints(t, db.DB, driver)
			if err := Migrate(t.Context(), db.DB, driver); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_dns_accounts WHERE id='binding' AND dns_account_id='account-a'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("重入迁移改变已有绑定", err)
			}
		})
	}
}

func TestSQLiteDomainDNSAccountGuards(t *testing.T) {
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
				seedDomainDNSMigration(t, db.DB)
				checkDomainDNSConstraints(t, db.DB, "sqlite")
				for name, want := range map[string]int{"foreign_keys": foreignKeys, "recursive_triggers": recursive} {
					var value int
					if err := db.DB.QueryRowContext(t.Context(), "PRAGMA "+name).Scan(&value); err != nil || value != want {
						t.Fatal("迁移不应修改连接设置", name, err)
					}
				}
			})
		}
	}
}

func seedDomainDNSMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO projects (id,name,description) VALUES ('binding-other','另一个项目','')`,
		`INSERT INTO domains (id,name,certificate_type,sans_json,status,project_id) VALUES ('domain-a','bound.example.test','single','[]','valid','default')`,
		`INSERT INTO domains (id,name,certificate_type,sans_json,status,project_id) VALUES ('domain-b','other.example.test','single','[]','valid','binding-other')`,
		`INSERT INTO dns_accounts VALUES ('account-a','账户','default','old-owner','cloudflare','secret://dns/account-a','active','verified',8,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO dns_accounts VALUES ('account-b','其他账户','binding-other','old-owner','cloudflare','secret://dns/account-b','active','verified',2,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func checkDomainDNSConstraints(t *testing.T, db *sql.DB, driver string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO domain_dns_accounts VALUES ('domain-a','binding','default','account-a',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO domain_dns_accounts VALUES ('domain-b','cross-domain','default','account-a',CURRENT_TIMESTAMP)`,
		`INSERT INTO domain_dns_accounts VALUES ('domain-b','cross-account','binding-other','account-a',CURRENT_TIMESTAMP)`,
		`INSERT INTO domain_dns_accounts VALUES ('missing','orphan','default','account-a',CURRENT_TIMESTAMP)`,
		`UPDATE domain_dns_accounts SET dns_account_id='account-b' WHERE id='binding'`,
		`UPDATE domain_dns_accounts SET domain_id='domain-b' WHERE id='binding'`,
		`UPDATE domain_dns_accounts SET project_id='binding-other' WHERE id='binding'`,
		`DELETE FROM dns_accounts WHERE id='account-a'`,
		`DELETE FROM domains WHERE id='domain-a'`,
		`UPDATE dns_accounts SET id='moved' WHERE id='account-a'`,
		`UPDATE dns_accounts SET project_id='binding-other' WHERE id='account-a'`,
		`UPDATE domains SET id='moved' WHERE id='domain-a'`,
		`UPDATE domains SET project_id='binding-other' WHERE id='domain-a'`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err == nil {
			t.Fatal("数据库未阻止破坏引用：", statement)
		}
	}
	if driver == "sqlite" {
		for _, statement := range []string{
			`INSERT OR REPLACE INTO domains (id,name,certificate_type,sans_json,status) VALUES ('domain-a','new.example.test','single','[]','pending')`,
			`INSERT OR REPLACE INTO domains (id,name,certificate_type,sans_json,status) VALUES ('new-domain','bound.example.test','single','[]','pending')`,
			`INSERT OR REPLACE INTO dns_accounts VALUES ('account-a','替换','default','owner','cloudflare','secret://dns/new','active','verified',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
			`INSERT OR REPLACE INTO dns_accounts VALUES ('new-account','替换','default','owner','cloudflare','secret://dns/account-a','active','verified',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		} {
			if _, err := db.ExecContext(t.Context(), statement); err == nil {
				t.Fatal("替换操作破坏绑定父记录", statement)
			}
		}
	}
	var revision int
	var status string
	if err := db.QueryRowContext(t.Context(), `SELECT revision FROM dns_accounts WHERE id='account-a'`).Scan(&revision); err != nil || revision != 8 {
		t.Fatal("约束拒绝改变账户数据", err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT status FROM domains WHERE id='domain-a'`).Scan(&status); err != nil || status != "valid" {
		t.Fatal("升级或拒绝改变域名状态", err)
	}
}

func TestMySQLDomainDNSAccountInterruptedMigration(t *testing.T) {
	f := startRealMySQL(t)
	for _, boundary := range []string{"before_create", "after_create", "after_index", "after_domain_fk", "after_account_fk", "with_binding", "bad_column", "bad_index", "bad_foreign_key", "extra_foreign_key"} {
		t.Run(boundary, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 27)
			statements := mysqlDeploymentStatements(t, 28)
			if boundary != "before_create" {
				mysqlDeploymentExec(t, db.DB, statements[0])
			}
			if boundary == "after_index" || boundary == "after_domain_fk" || boundary == "after_account_fk" || boundary == "with_binding" {
				mysqlDeploymentExec(t, db.DB, statements[1])
			}
			if boundary == "after_domain_fk" || boundary == "after_account_fk" || boundary == "with_binding" {
				mysqlDeploymentExec(t, db.DB, statements[2])
			}
			if boundary == "after_account_fk" || boundary == "with_binding" {
				mysqlDeploymentExec(t, db.DB, statements[3])
			}
			conflict := true
			switch boundary {
			case "bad_column":
				mysqlDeploymentExec(t, db.DB, `ALTER TABLE domain_dns_accounts MODIFY COLUMN id VARCHAR(36) NULL`)
			case "bad_index":
				mysqlDeploymentExec(t, db.DB, `CREATE INDEX uq_dns_accounts_project_id ON dns_accounts (project_id, id)`)
			case "bad_foreign_key":
				mysqlDeploymentExec(t, db.DB, `ALTER TABLE domain_dns_accounts ADD CONSTRAINT fk_domain_dns_domain FOREIGN KEY (project_id, domain_id) REFERENCES domains(project_id,id) ON DELETE CASCADE`)
			case "extra_foreign_key":
				mysqlDeploymentExec(t, db.DB, `ALTER TABLE domain_dns_accounts ADD CONSTRAINT unexpected_domain_dns FOREIGN KEY (project_id) REFERENCES projects(id)`)
			default:
				conflict = false
			}
			if boundary == "with_binding" {
				seedDomainDNSMigration(t, db.DB)
				mysqlDeploymentExec(t, db.DB, `INSERT INTO domain_dns_accounts VALUES ('domain-a','binding','default','account-a',CURRENT_TIMESTAMP)`)
			}
			for range 2 {
				err := Migrate(t.Context(), db.DB, "mysql")
				if (err != nil) != conflict {
					t.Fatal("中断恢复或冲突判断错误", err)
				}
				version, err := currentMigrationVersion(t.Context(), db.DB)
				if err != nil || (conflict && version != 27) || (!conflict && version != latestMigrationVersion) {
					t.Fatal("迁移版本提前登记", version, err)
				}
			}
			if boundary == "with_binding" {
				var account string
				if err := db.DB.QueryRowContext(t.Context(), `SELECT dns_account_id FROM domain_dns_accounts WHERE id='binding'`).Scan(&account); err != nil || account != "account-a" {
					t.Fatal("中断恢复改变原有绑定", err)
				}
			}
		})
	}
}
