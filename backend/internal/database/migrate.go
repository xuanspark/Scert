package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed migrations/sqlite/0001_initial.sql migrations/sqlite/0002_domain_options.sql migrations/sqlite/0003_deployment.sql migrations/sqlite/0004_task_attempts.sql migrations/sqlite/0005_users.sql migrations/sqlite/0006_server_validation.sql migrations/sqlite/0007_server_password_auth.sql migrations/sqlite/0009_server_ssh_key_status.sql migrations/mysql/0001_initial.sql migrations/mysql/0002_domain_options.sql migrations/mysql/0003_deployment.sql migrations/mysql/0004_task_attempts.sql migrations/mysql/0005_users.sql migrations/mysql/0006_server_validation.sql migrations/mysql/0007_server_password_auth.sql migrations/mysql/0009_server_ssh_key_status.sql migrations/postgres/0001_initial.sql migrations/postgres/0002_domain_options.sql migrations/postgres/0003_deployment.sql migrations/postgres/0004_task_attempts.sql migrations/postgres/0005_users.sql migrations/postgres/0006_server_validation.sql migrations/postgres/0007_server_password_auth.sql migrations/postgres/0009_server_ssh_key_status.sql
//go:embed migrations/sqlite/0008_user_groups.sql migrations/mysql/0008_user_groups.sql migrations/postgres/0008_user_groups.sql
//go:embed migrations/sqlite/0010_projects.sql migrations/mysql/0010_projects.sql migrations/postgres/0010_projects.sql
//go:embed migrations/sqlite/0011_resource_projects.sql migrations/mysql/0011_resource_projects.sql migrations/postgres/0011_resource_projects.sql
//go:embed migrations/sqlite/0012_resource_replacement.sql migrations/mysql/0012_resource_replacement.sql migrations/postgres/0012_resource_replacement.sql
//go:embed migrations/sqlite/0013_history_scope.sql migrations/mysql/0013_history_scope.sql migrations/postgres/0013_history_scope.sql
//go:embed migrations/sqlite/0014_project_members.sql migrations/mysql/0014_project_members.sql migrations/postgres/0014_project_members.sql
//go:embed migrations/sqlite/0015_task_idempotency.sql migrations/mysql/0015_task_idempotency.sql migrations/postgres/0015_task_idempotency.sql
//go:embed migrations/sqlite/0016_task_deployment_results.sql migrations/mysql/0016_task_deployment_results.sql migrations/postgres/0016_task_deployment_results.sql
//go:embed migrations/sqlite/0017_deployment_transactions.sql migrations/mysql/0017_deployment_transactions.sql migrations/postgres/0017_deployment_transactions.sql
//go:embed migrations/sqlite/0018_recovery_evidence.sql migrations/mysql/0018_recovery_evidence.sql migrations/postgres/0018_recovery_evidence.sql
var migrationFiles embed.FS

func Migrate(ctx context.Context, db *sql.DB, driver string) error {
	if db == nil {
		return fmt.Errorf("数据库连接不能为空")
	}
	if driver != "sqlite" && driver != "mysql" && driver != "postgres" {
		return fmt.Errorf("数据库驱动无效：%s", driver)
	}
	if _, err := db.ExecContext(ctx, migrationTableSQL(driver)); err != nil {
		return fmt.Errorf("创建迁移表失败：%w", err)
	}
	version, err := currentMigrationVersion(ctx, db)
	if err != nil {
		return err
	}
	for nextVersion := version + 1; nextVersion <= latestMigrationVersion; nextVersion++ {
		if err := applyMigration(ctx, db, driver, nextVersion); err != nil {
			return err
		}
	}
	return nil
}

const latestMigrationVersion = 18

// currentMigrationVersion 在执行业务迁移前拒绝新版数据库及不连续的迁移历史。
func currentMigrationVersion(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return 0, fmt.Errorf("读取迁移版本失败：%w", err)
	}
	defer rows.Close()
	current := 0
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return 0, fmt.Errorf("读取迁移版本失败：%w", err)
		}
		if version > latestMigrationVersion {
			return 0, fmt.Errorf("数据库版本 %d 高于当前程序支持的版本 %d，拒绝启动；请使用兼容版本程序，不要删除迁移记录", version, latestMigrationVersion)
		}
		if version != current+1 {
			return 0, fmt.Errorf("数据库迁移历史不连续：期望版本 %d，实际版本 %d，拒绝继续迁移", current+1, version)
		}
		current = version
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("读取迁移版本失败：%w", err)
	}
	return current, nil
}

func applyMigration(ctx context.Context, db *sql.DB, driver string, version int) error {
	path := fmt.Sprintf("migrations/%s/%04d_%s.sql", driver, version, migrationName(version))
	content, err := migrationFiles.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取迁移文件失败：%w", err)
	}
	statements := migrationStatements(content)
	if driver == "sqlite" && version == 4 {
		return applySQLiteTaskMigration(ctx, db, statements, version)
	}
	if driver == "mysql" {
		if version >= 15 && version <= 17 {
			return applyMySQLDeploymentMigration(ctx, db, version, statements)
		}
		if version == 14 {
			if err := installMySQLProjectMembers(ctx, db); err != nil {
				return err
			}
		}
		for _, statement := range statements {
			skip, err := shouldSkipMySQLStatement(ctx, db, statement)
			if err != nil {
				return fmt.Errorf("检查数据库迁移状态失败：%w", err)
			}
			if skip {
				continue
			}
			if _, err := db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("执行数据库迁移失败：%w", err)
			}
		}
		if version == 13 {
			if err := backfillHistoryScope(ctx, db); err != nil {
				return err
			}
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
			return fmt.Errorf("记录迁移版本失败：%w", err)
		}
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始迁移事务失败：%w", err)
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("执行数据库迁移失败：%w", err)
		}
	}
	if driver == "sqlite" && version == 11 {
		if err := installSQLiteResourceConstraints(ctx, tx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if driver == "sqlite" && version == 12 {
		if err := installSQLiteReplacementGuards(ctx, tx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if version == 13 {
		if err := backfillHistoryScope(ctx, tx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if driver == "sqlite" && version == 14 {
		if err := installSQLiteProjectMemberGuards(ctx, tx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	migrationVersionInsert := "INSERT INTO schema_migrations (version) VALUES (?)"
	if driver == "postgres" {
		migrationVersionInsert = "INSERT INTO schema_migrations (version) VALUES ($1)"
	}
	if _, err := tx.ExecContext(ctx, migrationVersionInsert, version); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("记录迁移版本失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交数据库迁移失败：%w", err)
	}
	return nil
}

var (
	mysqlAddColumnPattern     = regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+` + "`?([a-zA-Z0-9_]+)`?" + `\s+ADD\s+COLUMN\s+` + "`?([a-zA-Z0-9_]+)`?")
	mysqlCreateIndexPattern   = regexp.MustCompile(`(?is)^CREATE\s+(?:UNIQUE\s+)?INDEX\s+` + "`?([a-zA-Z0-9_]+)`?" + `\s+ON\s+` + "`?([a-zA-Z0-9_]+)`?")
	mysqlAddConstraintPattern = regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+` + "`?([a-zA-Z0-9_]+)`?" + `\s+ADD\s+CONSTRAINT\s+` + "`?([a-zA-Z0-9_]+)`?")
)

// shouldSkipMySQLStatement 检查可恢复迁移中的字段和索引是否已经存在。
// MySQL 5.7 不支持 ADD COLUMN IF NOT EXISTS，因此需要在执行前查询元数据。
func shouldSkipMySQLStatement(ctx context.Context, db *sql.DB, statement string) (bool, error) {
	statement = strings.TrimSpace(statement)
	if matches := mysqlAddConstraintPattern.FindStringSubmatch(statement); len(matches) == 3 {
		var count int
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.table_constraints
			WHERE constraint_schema = DATABASE() AND table_name = ? AND constraint_name = ?`, matches[1], matches[2]).Scan(&count)
		return count > 0, err
	}
	if matches := mysqlAddColumnPattern.FindStringSubmatch(statement); len(matches) == 3 {
		var count int
		err := db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`, matches[1], matches[2]).Scan(&count)
		return count > 0, err
	}
	if matches := mysqlCreateIndexPattern.FindStringSubmatch(statement); len(matches) == 3 {
		var count int
		err := db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM information_schema.statistics
			WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`, matches[2], matches[1]).Scan(&count)
		return count > 0, err
	}
	return false, nil
}

func migrationName(version int) string {
	switch version {
	case 1:
		return "initial"
	case 2:
		return "domain_options"
	case 3:
		return "deployment"
	case 4:
		return "task_attempts"
	case 5:
		return "users"
	case 6:
		return "server_validation"
	case 7:
		return "server_password_auth"
	case 8:
		return "user_groups"
	case 9:
		return "server_ssh_key_status"
	case 10:
		return "projects"
	case 11:
		return "resource_projects"
	case 12:
		return "resource_replacement"
	case 13:
		return "history_scope"
	case 14:
		return "project_members"
	case 15:
		return "task_idempotency"
	case 16:
		return "task_deployment_results"
	case 17:
		return "deployment_transactions"
	case 18:
		return "recovery_evidence"
	default:
		return "unknown"
	}
}

func migrationStatements(content []byte) []string {
	statements := make([]string, 0)
	// 只移除文件头的编码标记，不能替换 SQL 字面量中的相同字符。
	script := strings.TrimPrefix(string(content), "\ufeff")
	for _, statement := range strings.Split(script, ";") {
		statement = strings.TrimSpace(statement)
		if statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

func migrationTableSQL(driver string) string {
	if driver == "mysql" {
		return `CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT NOT NULL PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`
	}
	if driver == "postgres" {
		return `CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER NOT NULL PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`
	}
	return `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
}
