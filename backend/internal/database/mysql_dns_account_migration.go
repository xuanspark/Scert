package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// MySQL 的建表和外键各自提交；重入时逐项比对，结构冲突不覆盖历史数据。
func applyMySQLDNSAccountMigration(ctx context.Context, db *sql.DB, version int, statements []string) error {
	if len(statements) != 2 {
		return fmt.Errorf("DNS 账户迁移语句不完整")
	}
	shape, err := parseMySQLDeploymentShape(statements[0])
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, strings.Replace(statements[0], "CREATE TABLE ", "CREATE TABLE IF NOT EXISTS ", 1)); err != nil {
		return fmt.Errorf("创建 DNS 账户表失败：%w", err)
	}
	if err := validateMySQLDeploymentShape(ctx, db, shape); err != nil {
		return err
	}
	// 先检查已有外键，避免把同名但不同语义的外键当作迁移成功。
	count, err := validateMySQLDNSAccountForeignKey(ctx, db)
	if err != nil {
		return err
	}
	if count == 0 {
		if _, err := db.ExecContext(ctx, statements[1]); err != nil {
			return fmt.Errorf("创建 DNS 账户项目约束失败：%w", err)
		}
	}
	if count, err = validateMySQLDNSAccountForeignKey(ctx, db); err != nil || count != 1 {
		return fmt.Errorf("DNS 账户项目约束校验失败：%v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
		return fmt.Errorf("记录 DNS 账户迁移版本失败：%w", err)
	}
	return nil
}

func validateMySQLDNSAccountForeignKey(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT r.constraint_name, r.delete_rule, r.update_rule,
		k.column_name, k.referenced_table_schema, k.referenced_table_name, k.referenced_column_name,
		k.ordinal_position, DATABASE()
		FROM information_schema.referential_constraints r
		JOIN information_schema.key_column_usage k ON k.constraint_schema = r.constraint_schema
		AND k.table_name = r.table_name AND k.constraint_name = r.constraint_name
		WHERE r.constraint_schema = DATABASE() AND r.table_name = 'dns_accounts'`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name, onDelete, onUpdate, column, schema, table, target, currentSchema string
		var position int
		if err := rows.Scan(&name, &onDelete, &onUpdate, &column, &schema, &table, &target, &position, &currentSchema); err != nil {
			return 0, err
		}
		count++
		if count != 1 || name != "fk_dns_accounts_project" || onDelete != "RESTRICT" || (onUpdate != "RESTRICT" && onUpdate != "NO ACTION") || column != "project_id" || schema != currentSchema || table != "projects" || target != "id" || position != 1 {
			return 0, fmt.Errorf("DNS 账户项目外键与预期不一致，拒绝记录迁移版本")
		}
	}
	return count, rows.Err()
}
