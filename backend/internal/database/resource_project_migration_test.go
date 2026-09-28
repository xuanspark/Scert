package database

import (
	"fmt"
	"testing"
)

func TestSQLiteResourceProjectMigration(t *testing.T) {
	for _, foreignKeys := range []int{0, 1} {
		t.Run(fmt.Sprintf("foreign_keys_%d", foreignKeys), func(t *testing.T) {
			db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.DB.SetMaxOpenConns(1)
			createSQLiteHistoricalBusiness(t, db.DB, 10)
			for _, query := range []string{
				fmt.Sprintf("PRAGMA foreign_keys = %d", foreignKeys),
				`CREATE VIEW resource_names AS SELECT id, name FROM domains`,
				`CREATE TABLE resource_events (name TEXT)`,
				`CREATE TRIGGER resource_name_event AFTER UPDATE OF name ON domains BEGIN INSERT INTO resource_events VALUES (new.name); END`,
				`CREATE INDEX custom_resource_name ON domains(name, id)`,
			} {
				if _, err := db.DB.ExecContext(t.Context(), query); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotSQLiteHistoricalBusiness(t, db.DB)
			objectsQuery := `SELECT type, name, sql FROM sqlite_master WHERE name IN ('resource_names', 'resource_events', 'resource_name_event', 'custom_resource_name') ORDER BY name`
			objects := snapshotSQLiteUpgradeQuery(t, db.DB, objectsQuery)
			for round := 0; round < 2; round++ {
				if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
					t.Fatal(err)
				}
			}
			assertSQLiteHistoricalBusiness(t, db.DB, before)
			if actual := snapshotSQLiteUpgradeQuery(t, db.DB, objectsQuery); actual != objects {
				t.Fatal("资源升级改写已有视图、索引或触发器")
			}
			for _, table := range []string{"domains", "servers"} {
				var project, owner, creator, updater string
				if err := db.DB.QueryRowContext(t.Context(), "SELECT project_id, owner_id, created_by, updated_by FROM "+table).Scan(&project, &owner, &creator, &updater); err != nil {
					t.Fatal(err)
				}
				if project != "default" || owner != "" || creator != "" || updater != "" {
					t.Fatal("历史归属回填不正确，不能编造历史操作者")
				}
			}
			for _, statement := range []string{
				`UPDATE domains SET project_id = 'missing'`,
				`DELETE FROM projects WHERE id = 'default'`,
				`UPDATE domain_servers SET server_id = 'missing'`,
				`INSERT OR REPLACE INTO domains (id, name, certificate_type, sans_json, status) SELECT 'replacement', name, certificate_type, sans_json, status FROM domains WHERE id = 'domain'`,
				`INSERT OR REPLACE INTO domains (id, name, certificate_type, sans_json, status) SELECT id, name, certificate_type, sans_json, status FROM domains WHERE id = 'domain'`,
				`INSERT OR REPLACE INTO servers (id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json) SELECT id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json FROM servers`,
			} {
				if _, err := db.DB.ExecContext(t.Context(), statement); err == nil {
					t.Errorf("资源约束被绕过：%s", statement)
				}
			}
			assertSQLiteHistoricalBusiness(t, db.DB, before)
			var enabled int
			if err := db.DB.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != foreignKeys {
				t.Fatalf("迁移改变原连接外键配置：%d，%v", enabled, err)
			}
			if _, err := db.DB.ExecContext(t.Context(), `UPDATE domains SET name = 'after.example.test' WHERE id = 'domain'`); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resource_events").Scan(&count); err != nil || count != 1 {
				t.Fatalf("已有触发器升级后未执行：%d，%v", count, err)
			}
		})
	}
}

func TestSQLiteResourceProjectMigrationRollback(t *testing.T) {
	for _, failure := range []struct{ name, setup, repair string }{
		{"version", `CREATE TRIGGER resource_version_failure BEFORE INSERT ON schema_migrations WHEN new.version = 11 BEGIN SELECT RAISE(ABORT, 'test-only'); END`, `DROP TRIGGER resource_version_failure`},
		{"orphan_domain", `INSERT INTO domain_servers (domain_id, server_id) VALUES ('missing', 'server')`, `DELETE FROM domain_servers WHERE domain_id = 'missing'`},
		{"orphan_server", `INSERT INTO domain_servers (domain_id, server_id) VALUES ('domain', 'missing')`, `DELETE FROM domain_servers WHERE server_id = 'missing'`},
		{"missing_project", `DELETE FROM projects WHERE id = 'default'`, `INSERT INTO projects (id, name, description) VALUES ('default', '默认项目', '')`},
	} {
		t.Run(failure.name, func(t *testing.T) {
			db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			createSQLiteHistoricalBusiness(t, db.DB, 10)
			if _, err := db.DB.ExecContext(t.Context(), failure.setup); err != nil {
				t.Fatal(err)
			}
			before := snapshotSQLiteHistoricalBusiness(t, db.DB)
			schemaQuery := `SELECT type, name, sql FROM sqlite_master ORDER BY name`
			schema := snapshotSQLiteUpgradeQuery(t, db.DB, schemaQuery)
			if err := Migrate(t.Context(), db.DB, "sqlite"); err == nil {
				t.Fatal("故障未阻止资源迁移")
			}
			assertSQLiteHistoricalBusiness(t, db.DB, before)
			if actual := snapshotSQLiteUpgradeQuery(t, db.DB, schemaQuery); actual != schema {
				t.Fatal("资源迁移失败后未完整回滚模式")
			}
			version, err := currentMigrationVersion(t.Context(), db.DB)
			if err != nil || version != 10 {
				t.Fatalf("资源迁移失败仍登记版本：%d，%v", version, err)
			}
			if _, err := db.DB.ExecContext(t.Context(), failure.repair); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
