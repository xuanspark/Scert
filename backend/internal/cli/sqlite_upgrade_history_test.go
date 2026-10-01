package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
)

// 以当批源码目录为预期，同时校验正式程序写入的迁移历史连续且完整。
func expectedSQLiteCLIMigrationVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "database", "migrations", "sqlite"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("无法读取本批 SQLite 迁移目录：%v", err)
	}
	for index, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), fmt.Sprintf("%04d_", index+1)) || !strings.HasSuffix(entry.Name(), ".sql") {
			t.Fatalf("SQLite 迁移文件序列无效：%s", entry.Name())
		}
	}
	rows, err := db.QueryContext(t.Context(), "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		count++
		if version != count {
			t.Fatalf("正式程序迁移历史不连续：期望 %d，实际 %d", count, version)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != len(entries) {
		t.Fatalf("正式程序迁移不完整：源码 %d 项，数据库 %d 项", len(entries), count)
	}
	return len(entries)
}

// 仅在隔离样本中写入终态事务，用于验证存储恢复，不作为远端恢复证据。
func seedSQLiteCLIDeploymentHistory(t *testing.T, fixture sqliteBusinessFixture) {
	t.Helper()
	db, err := database.Open(t.Context(), fixture.config.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`INSERT INTO task_idempotency (domain_id, idempotency_key, task_id) VALUES ('domain', 'backup-deploy-key', 'task')`,
		`INSERT INTO task_attempts (id, task_id, attempt_number, stage, status, message) VALUES ('backup-attempt', 'task', 1, 'certificate_deploy', 'success', '历史部署尝试')`,
		`INSERT INTO task_deployment_results (task_id, attempt_id, attempt_number, result_index, server_id, server_name, status, message, backup_id) VALUES ('task', 'backup-attempt', 1, 0, 'server', '历史服务器', 'success', '历史部署结果', 'backup-transaction')`,
		`INSERT INTO deployment_transactions (id, task_id, attempt_id, attempt_number, result_index, server_id, server_name, certificate_path, certificate_chain_path, private_key_path, temporary_paths_json, installed_paths_json, backups_json, lock_paths_json, reload_attempted, phase, status, message, recovery_evidence_json) VALUES ('backup-transaction', 'task', 'backup-attempt', 1, 0, 'server', '历史服务器', '/test/cert.pem', '/test/chain.pem', '/test/key.pem', '[]', '["/test/cert.pem"]', '[]', '[]', 1, 'committed', 'committed', '历史事务', '{"version":1,"lock_token":"fixture-lock","address":"192.0.2.10","port":22}')`,
	} {
		if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

// 主动改变新增表，避免用空表或未变化的数据宣称恢复成功。
func mutateSQLiteCLIDeploymentHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`UPDATE task_idempotency SET idempotency_key = 'changed-key'`,
		`UPDATE task_attempts SET message = '待恢复尝试'`,
		`UPDATE task_deployment_results SET message = '待恢复结果'`,
		`UPDATE deployment_transactions SET recovery_evidence_json = '{}', message = '待恢复证据'`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}
