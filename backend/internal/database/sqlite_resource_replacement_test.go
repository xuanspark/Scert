package database

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestSQLiteResourceProjectReplacementGuards(t *testing.T) {
	cases := []struct{ name, statement string }{
		{"domain_id", `UPDATE OR REPLACE domains SET id = 'domain' WHERE id = 'donor-domain'`},
		{"server_id", `UPDATE OR REPLACE servers SET id = 'server' WHERE id = 'donor-server'`},
		{"project_id", `UPDATE OR REPLACE projects SET id = 'default' WHERE id = 'donor-project'`},
		{"project_insert", `INSERT OR REPLACE INTO projects (id, name, description) VALUES ('default', '替换项目', '')`},
		{"domain_rowid", `UPDATE OR REPLACE domains SET rowid = (SELECT rowid FROM domains WHERE id = 'domain') WHERE id = 'donor-domain'`},
		{"server_rowid", `UPDATE OR REPLACE servers SET _rowid_ = (SELECT rowid FROM servers WHERE id = 'server') WHERE id = 'donor-server'`},
		{"project_rowid", `UPDATE OR REPLACE projects SET oid = (SELECT rowid FROM projects WHERE id = 'default') WHERE id = 'donor-project'`},
		{"domain_insert_rowid", `INSERT OR REPLACE INTO domains (rowid, id, name, certificate_type, sans_json, status) SELECT rowid, 'replacement-domain', 'replacement.example.test', certificate_type, sans_json, status FROM domains WHERE id = 'domain'`},
		{"server_insert_rowid", `INSERT OR REPLACE INTO servers (rowid, id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json) SELECT rowid, 'replacement-server', name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json FROM servers WHERE id = 'server'`},
		{"project_insert_rowid", `INSERT OR REPLACE INTO projects (rowid, id, name, description) SELECT rowid, 'replacement-project', name, description FROM projects WHERE id = 'default'`},
	}
	for _, foreignKeys := range []int{0, 1} {
		for _, recursive := range []int{0, 1} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("fk_%d_recursive_%d/%s", foreignKeys, recursive, tc.name), func(t *testing.T) {
					db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					db.DB.SetMaxOpenConns(1)
					createSQLiteHistoricalBusiness(t, db.DB, 11)
					for _, statement := range []string{
						fmt.Sprintf("PRAGMA foreign_keys = %d", foreignKeys),
						fmt.Sprintf("PRAGMA recursive_triggers = %d", recursive),
						`INSERT INTO projects (id, name, description) VALUES ('donor-project', '无资源项目', '')`,
						`INSERT INTO domains (id, name, certificate_type, sans_json, status) VALUES ('donor-domain', 'donor.example.test', 'single', '[]', 'pending')`,
						`INSERT INTO servers (id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json) SELECT 'donor-server', name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json FROM servers WHERE id = 'server'`,
					} {
						if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
							t.Fatal(err)
						}
					}
					before := snapshotSQLiteReplacementState(t, db.DB)
					if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
						t.Fatal(err)
					}
					assertSQLiteHistoricalBusiness(t, db.DB, before)
					if _, err := db.DB.ExecContext(t.Context(), tc.statement); err == nil {
						t.Fatal("替换操作绕过了资源引用保护")
					}
					assertSQLiteHistoricalBusiness(t, db.DB, before)
					for pragma, want := range map[string]int{"foreign_keys": foreignKeys, "recursive_triggers": recursive} {
						var actual int
						if err := db.DB.QueryRowContext(t.Context(), "PRAGMA "+pragma).Scan(&actual); err != nil || actual != want {
							t.Fatalf("替换保护改变连接配置 %s：%d，%v", pragma, actual, err)
						}
					}
				})
			}
		}
	}
}

func snapshotSQLiteReplacementState(t *testing.T, db *sql.DB) map[string]sqliteHistoricalTable {
	t.Helper()
	result := snapshotSQLiteHistoricalBusiness(t, db)
	for _, table := range []string{"projects", "domains", "servers"} {
		query := "SELECT rowid, * FROM " + table + " ORDER BY id"
		result[table+"_with_rowid"] = sqliteHistoricalTable{query: query, rows: snapshotSQLiteUpgradeQuery(t, db, query)}
	}
	return result
}

func TestSQLiteResourceProjectReplacementAllowsValidWrites(t *testing.T) {
	db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.DB.SetMaxOpenConns(1)
	createSQLiteHistoricalBusiness(t, db.DB, 11)
	if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE domains SET name = 'edited.example.test' WHERE id = 'domain'`,
		`UPDATE servers SET name = '编辑服务器' WHERE id = 'server'`,
		`UPDATE projects SET name = '编辑项目' WHERE id = 'default'`,
		`UPDATE domains SET rowid = -1 WHERE id = 'domain'`,
		`UPDATE servers SET _rowid_ = -1 WHERE id = 'server'`,
		`UPDATE projects SET oid = -1 WHERE id = 'default'`,
		`INSERT INTO projects (id, name, description) VALUES ('unreferenced', '无引用项目', '')`,
		`INSERT OR REPLACE INTO projects (id, name, description) VALUES ('unreferenced', '允许替换', '')`,
		`INSERT INTO domains (id, name, certificate_type, sans_json, status) VALUES ('new-domain', 'new.example.test', 'single', '[]', 'pending')`,
		`INSERT OR REPLACE INTO domains (id, name, certificate_type, sans_json, status) VALUES ('new-domain', 'new.example.test', 'single', '[]', 'pending')`,
		`INSERT INTO servers (id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json) SELECT 'new-server', name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json FROM servers WHERE id = 'server'`,
	} {
		if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("正常写入被替换保护误拦：%s，%v", statement, err)
		}
	}
	var count int
	if err := db.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_servers b JOIN domains d ON d.id = b.domain_id AND d.project_id = b.project_id JOIN servers s ON s.id = b.server_id AND s.project_id = b.project_id`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("正常写入破坏原有绑定：%d，%v", count, err)
	}
}
