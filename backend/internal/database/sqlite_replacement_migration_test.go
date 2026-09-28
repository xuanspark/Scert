package database

import "testing"

func TestSQLiteResourceProjectReplacementMigrationRollback(t *testing.T) {
	cases := []struct{ name, setup, repair string }{
		{"version_record", `CREATE TRIGGER fail_replacement_version BEFORE INSERT ON schema_migrations WHEN new.version = 12 BEGIN SELECT RAISE(ABORT, 'test-only'); END`, `DROP TRIGGER fail_replacement_version`},
		{"trigger_collision", `CREATE TRIGGER scert_servers_replace_update_guard BEFORE UPDATE ON servers BEGIN SELECT 1; END`, `DROP TRIGGER scert_servers_replace_update_guard`},
		{"orphan_binding", `INSERT OR REPLACE INTO domains (rowid, id, name, certificate_type, sans_json, status) SELECT rowid, 'replacement', 'replacement.example.test', certificate_type, sans_json, status FROM domains WHERE id = 'domain'`, `UPDATE domains SET id = 'domain' WHERE id = 'replacement'`},
		{"orphan_server", `INSERT OR REPLACE INTO servers (rowid, id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json) SELECT rowid, 'replacement', name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json FROM servers WHERE id = 'server'`, `UPDATE servers SET id = 'server' WHERE id = 'replacement'`},
		{"missing_project", `INSERT OR REPLACE INTO projects (rowid, id, name, description) SELECT rowid, 'replacement', name, description FROM projects WHERE id = 'default'`, `UPDATE projects SET id = 'default' WHERE id = 'replacement'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.DB.SetMaxOpenConns(1)
			createSQLiteHistoricalBusiness(t, db.DB, 11)
			if _, err := db.DB.ExecContext(t.Context(), "PRAGMA recursive_triggers = 0"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.DB.ExecContext(t.Context(), tc.setup); err != nil {
				t.Fatal(err)
			}
			before := snapshotSQLiteReplacementState(t, db.DB)
			schemaQuery := `SELECT type, name, sql FROM sqlite_master ORDER BY name`
			schema := snapshotSQLiteUpgradeQuery(t, db.DB, schemaQuery)
			if err := Migrate(t.Context(), db.DB, "sqlite"); err == nil {
				t.Fatal("资源替换保护迁移未拒绝故障")
			}
			assertSQLiteHistoricalBusiness(t, db.DB, before)
			if actual := snapshotSQLiteUpgradeQuery(t, db.DB, schemaQuery); actual != schema {
				t.Fatal("迁移失败未完整回滚原模式")
			}
			version, err := currentMigrationVersion(t.Context(), db.DB)
			if err != nil || version != 11 {
				t.Fatalf("失败迁移错误登记版本：%d，%v", version, err)
			}
			if _, err := db.DB.ExecContext(t.Context(), tc.repair); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
					t.Fatal(err)
				}
			}
			var count int
			if err := db.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name IN ('scert_domains_replace_update_guard', 'scert_domains_replace_insert_guard', 'scert_servers_replace_update_guard', 'scert_servers_replace_insert_guard', 'scert_project_replace_insert_guard', 'scert_project_replace_update_guard', 'scert_project_replace_reference_guard')`).Scan(&count); err != nil || count != 7 {
				t.Fatalf("重试未完整建立七项保护：%d，%v", count, err)
			}
		})
	}
}
