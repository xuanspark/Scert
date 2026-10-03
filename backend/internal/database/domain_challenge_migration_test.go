package database

import (
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/postgrestest"
)

func TestDomainChallengeMigration(t *testing.T) {
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
			for version := 1; version <= 25; version++ {
				if err := applyMigration(t.Context(), db.DB, driver, version); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO domains (id,name,certificate_type,sans_json,status) VALUES ('legacy','legacy.example.com','single','["legacy.example.com"]','valid')`); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				if err := Migrate(t.Context(), db.DB, driver); err != nil {
					t.Fatal(err)
				}
				var kind, status string
				if err := db.DB.QueryRowContext(t.Context(), `SELECT challenge_type,status FROM domains WHERE id='legacy'`).Scan(&kind, &status); err != nil {
					t.Fatal(err)
				}
				if kind != "dns-01" || status != "valid" {
					t.Fatal("升级改变历史域名验证方式或状态")
				}
			}
			if _, err := db.DB.ExecContext(t.Context(), `UPDATE domains SET challenge_type='http-01' WHERE id='legacy'`); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(t.Context(), db.DB, driver); err != nil {
				t.Fatal(err)
			}
			var kind string
			if err := db.DB.QueryRowContext(t.Context(), `SELECT challenge_type FROM domains WHERE id='legacy'`).Scan(&kind); err != nil || kind != "http-01" {
				t.Fatal("重复迁移覆盖 HTTP 验证选择：", err)
			}
		})
	}
}

func TestMySQLDomainChallengeInterruptedMigration(t *testing.T) {
	f := startRealMySQL(t)
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "conflict"}[conflict], func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 25)
			statement := "ALTER TABLE domains ADD COLUMN challenge_type VARCHAR(16) NOT NULL DEFAULT 'dns-01'"
			if conflict {
				statement = "ALTER TABLE domains ADD COLUMN challenge_type VARCHAR(8) NULL DEFAULT NULL"
			}
			if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			err := Migrate(t.Context(), db.DB, "mysql")
			if (err != nil) != conflict {
				t.Fatal("中断迁移结构检查结果错误：", err)
			}
			version, err := currentMigrationVersion(t.Context(), db.DB)
			if err != nil {
				t.Fatal(err)
			}
			if conflict && version != 25 || !conflict && version != latestMigrationVersion {
				t.Fatal("迁移版本记录错误")
			}
		})
	}
}
