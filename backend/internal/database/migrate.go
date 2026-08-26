package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
)

//go:embed migrations/sqlite/0001_initial.sql migrations/mysql/0001_initial.sql
var migrationFiles embed.FS

func Migrate(ctx context.Context, db *sql.DB, driver string) error {
	if db == nil {
		return fmt.Errorf("数据库连接不能为空")
	}
	if driver != "sqlite" && driver != "mysql" {
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
	if version >= 1 {
		return nil
	}
	path := "migrations/sqlite/0001_initial.sql"
	if driver == "mysql" {
		path = "migrations/mysql/0001_initial.sql"
	}
	content, err := migrationFiles.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取迁移文件失败：%w", err)
	}
	statements := migrationStatements(content)
	if driver == "mysql" {
		for _, statement := range statements {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("执行数据库迁移失败：%w", err)
			}
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (1)"); err != nil {
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
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (1)"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("记录迁移版本失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交数据库迁移失败：%w", err)
	}
	return nil
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
	return `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
}
