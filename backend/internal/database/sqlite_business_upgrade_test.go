package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// 历史版本必须先写入业务数据；空表迁移不能证明真实升级可用。
func TestSQLiteBusinessUpgradeFromEveryVersion(t *testing.T) {
	for version := 1; version <= latestMigrationVersion; version++ {
		t.Run(fmt.Sprintf("version_%d", version), func(t *testing.T) {
			cfg := sqliteBusinessUpgradeConfig(t)
			db, err := Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			createSQLiteHistoricalBusiness(t, db.DB, version)
			before := snapshotSQLiteHistoricalBusiness(t, db.DB)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for round := 0; round < 2; round++ {
				if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
					t.Fatalf("历史版本 %d 第 %d 次升级失败：%v", version, round+1, err)
				}
				assertSQLiteHistoricalBusiness(t, db.DB, before)
			}
			var history int
			if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&history); err != nil || history != latestMigrationVersion {
				t.Fatalf("升级后的迁移历史不完整：%d，%v", history, err)
			}
			var timestamp time.Time
			if err := db.DB.QueryRowContext(t.Context(), "SELECT updated_at FROM tasks WHERE id = 'task'").Scan(&timestamp); err != nil || timestamp.IsZero() {
				t.Fatalf("历史任务更新时间不可用：%v", err)
			}
			if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO tasks (id, domain_id, task_type, status, message) VALUES ('new-task', 'domain', 'deploy', 'queued', '升级后任务')`); err != nil {
				t.Fatal(err)
			}
			if err := db.DB.QueryRowContext(t.Context(), "SELECT updated_at FROM tasks WHERE id = 'new-task'").Scan(&timestamp); err != nil || time.Since(timestamp) < -time.Second || time.Since(timestamp) > time.Minute {
				t.Fatalf("新任务必须保留当前时间默认值：%v", err)
			}
			result, err := db.DB.ExecContext(t.Context(), `INSERT INTO audit_logs (action, resource_type, resource_id, status, message) VALUES ('upgrade', 'task', 'new-task', 'success', '升级后审计')`)
			if err != nil {
				t.Fatal(err)
			}
			if id, err := result.LastInsertId(); err != nil || id != 18 {
				t.Fatalf("审计自增序列错误：%d，%v", id, err)
			}
			var check string
			if err := db.DB.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
				t.Fatalf("升级后完整性检查失败：%s，%v", check, err)
			}
		})
	}
}

func sqliteBusinessUpgradeConfig(t *testing.T) config.DatabaseConfig {
	t.Helper()
	cfg := config.Default(t.TempDir()).Database
	// Linux 默认安装路径不随 baseDir 改变，测试必须显式指定临时数据库。
	cfg.DSN = filepath.Join(t.TempDir(), "historical.db")
	return cfg
}

func createSQLiteHistoricalBusiness(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), migrationTableSQL("sqlite")); err != nil {
		t.Fatal(err)
	}
	for next := 1; next <= version; next++ {
		if err := applyMigration(t.Context(), db, "sqlite", next); err != nil {
			t.Fatal(err)
		}
	}
	statements := []string{
		`INSERT INTO settings (key, value) VALUES ('historical', '历史设置：测试')`,
		`INSERT INTO domains (id, name, certificate_type, sans_json, status, expires_at) VALUES ('domain', 'example.test', 'single', '["example.test"]', 'issued', '2030-01-01 00:00:00')`,
		`INSERT INTO tasks (id, domain_id, task_type, status, message, started_at, finished_at, created_at) VALUES ('task', 'domain', 'deploy', 'success', '历史部署任务', '2020-01-01 01:00:00', '2020-01-01 02:00:00', '2020-01-01 00:00:00')`,
		`INSERT INTO tasks (id, domain_id, task_type, status, message) VALUES ('pending', 'domain', 'renew', 'queued', '历史待执行任务')`,
		`INSERT INTO audit_logs (id, action, resource_type, resource_id, status, message) VALUES (17, 'deploy', 'domain', 'domain', 'success', '历史审计记录')`,
	}
	if version >= 2 {
		statements = append(statements, `UPDATE domains SET auto_renew = 0, acme_environment = 'production', dns_provider = 'alidns'`)
	}
	if version >= 3 {
		statements = append(statements,
			`INSERT INTO servers (id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json) VALUES ('server', '历史部署服务器', '192.0.2.10', 22, 'deploy', 'secret://ssh/historical', 'test-only', '/test/cert.pem', '/test/key.pem', '/test/chain.pem', 'nginx', '["nginx","-s","reload"]')`,
			`INSERT INTO domain_servers (domain_id, server_id) VALUES ('domain', 'server')`)
	}
	if version >= 4 {
		statements = append(statements,
			`UPDATE tasks SET stage = 'completed', operator_id = 'owner', certificate_version_id = 'historical-certificate', idempotency_key = 'historical-operation' WHERE id = 'task'`,
			`INSERT INTO task_attempts (id, task_id, attempt_number, stage, status, message) VALUES ('attempt', 'task', 1, 'deploy', 'success', '历史部署尝试')`)
	}
	if version >= 5 {
		statements = append(statements, `INSERT INTO users (id, username, email, password_hash, role) VALUES ('owner', 'historical-owner', 'owner@example.test', 'historical-hash', 'super_admin')`)
	}
	if version >= 6 {
		statements = append(statements, `UPDATE servers SET validation_command_json = '["nginx","-t"]'`)
	}
	if version >= 7 {
		statements = append(statements, `UPDATE servers SET auth_method = 'password', password_ref = 'secret://ssh/password'`)
	}
	if version >= 8 {
		statements = append(statements, `UPDATE users SET groups_json = '["历史分组"]'`)
	}
	if version >= 9 {
		statements = append(statements, `UPDATE servers SET ssh_key_status = 'deployed', ssh_key_fingerprint = 'test-fingerprint', ssh_key_deployed_at = '2020-01-01T00:00:00Z'`)
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

type sqliteHistoricalTable struct {
	query string
	rows  string
}

func snapshotSQLiteHistoricalBusiness(t *testing.T, db *sql.DB) map[string]sqliteHistoricalTable {
	t.Helper()
	result := make(map[string]sqliteHistoricalTable)
	for _, name := range []string{"settings", "domains", "tasks", "audit_logs", "servers", "domain_servers", "task_attempts", "users", "sqlite_sequence"} {
		var exists int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			continue
		}
		rows, err := db.QueryContext(t.Context(), `SELECT * FROM "`+name+`" LIMIT 0`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for index, column := range columns {
			columns[index] = `"` + strings.ReplaceAll(column, `"`, `""`) + `"`
		}
		query := "SELECT " + strings.Join(columns, ",") + ` FROM "` + name + `" ORDER BY 1`
		result[name] = sqliteHistoricalTable{query: query, rows: snapshotSQLiteUpgradeQuery(t, db, query)}
	}
	return result
}

func snapshotSQLiteUpgradeQuery(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func assertSQLiteHistoricalBusiness(t *testing.T, db *sql.DB, expected map[string]sqliteHistoricalTable) {
	t.Helper()
	for table, snapshot := range expected {
		if actual := snapshotSQLiteUpgradeQuery(t, db, snapshot.query); !reflect.DeepEqual(actual, snapshot.rows) {
			t.Errorf("历史表 %s 的原有业务字段发生变化", table)
		}
	}
}
