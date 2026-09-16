package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed migrations/sqlite/0001_initial.sql migrations/sqlite/0002_domain_options.sql migrations/sqlite/0003_deployment.sql migrations/sqlite/0004_task_attempts.sql migrations/sqlite/0005_users.sql migrations/sqlite/0006_server_validation.sql migrations/mysql/0001_initial.sql migrations/mysql/0002_domain_options.sql migrations/mysql/0003_deployment.sql migrations/mysql/0004_task_attempts.sql migrations/mysql/0005_users.sql migrations/mysql/0006_server_validation.sql migrations/postgres/0001_initial.sql migrations/postgres/0002_domain_options.sql migrations/postgres/0003_deployment.sql migrations/postgres/0004_task_attempts.sql migrations/postgres/0005_users.sql migrations/postgres/0006_server_validation.sql
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
	var version int
	err := db.QueryRowContext(ctx, "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("读取迁移版本失败：%w", err)
	}
	if err == sql.ErrNoRows {
		version = 0
	}
	for nextVersion := version + 1; nextVersion <= latestMigrationVersion; nextVersion++ {
		if err := applyMigration(ctx, db, driver, nextVersion); err != nil {
			return err
		}
	}
	return nil
}

const latestMigrationVersion = 6

func applyMigration(ctx context.Context, db *sql.DB, driver string, version int) error {
	path := fmt.Sprintf("migrations/%s/%04d_%s.sql", driver, version, migrationName(version))
	content, err := migrationFiles.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取迁移文件失败：%w", err)
	}
	statements := migrationStatements(content)
	if driver == "mysql" {
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
	mysqlAddColumnPattern   = regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+` + "`?([a-zA-Z0-9_]+)`?" + `\s+ADD\s+COLUMN\s+` + "`?([a-zA-Z0-9_]+)`?")
	mysqlCreateIndexPattern = regexp.MustCompile(`(?is)^CREATE\s+INDEX\s+` + "`?([a-zA-Z0-9_]+)`?" + `\s+ON\s+` + "`?([a-zA-Z0-9_]+)`?")
)

// shouldSkipMySQLStatement 检查可恢复迁移中的字段和索引是否已经存在。
// MySQL 5.7 不支持 ADD COLUMN IF NOT EXISTS，因此需要在执行前查询元数据。
func shouldSkipMySQLStatement(ctx context.Context, db *sql.DB, statement string) (bool, error) {
	statement = strings.TrimSpace(statement)
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
	default:
		return "unknown"
	}
}

func migrationStatements(content []byte) []string {
	statements := make([]string, 0)
	for _, statement := range strings.Split(string(content), ";") {
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
