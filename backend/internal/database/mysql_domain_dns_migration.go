package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// MySQL DDL 不支持事务回滚，每一步重入均检查真实结构；不覆盖冲突表或外键。
func applyMySQLDomainDNSMigration(ctx context.Context, db *sql.DB, version int, statements []string) error {
	if len(statements) != 4 {
		return fmt.Errorf("域名 DNS 绑定迁移不完整")
	}
	shape, err := parseMySQLDeploymentShape(statements[0])
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, strings.Replace(statements[0], "CREATE TABLE ", "CREATE TABLE IF NOT EXISTS ", 1)); err != nil {
		return err
	}
	if err := validateMySQLDeploymentShape(ctx, db, shape); err != nil {
		return err
	}
	var indexCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = 'dns_accounts' AND index_name = 'uq_dns_accounts_project_id'`).Scan(&indexCount); err != nil {
		return err
	}
	if indexCount == 0 {
		if _, err := db.ExecContext(ctx, statements[1]); err != nil {
			return err
		}
		indexCount = 2
	}
	var validIndex int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = 'dns_accounts' AND index_name = 'uq_dns_accounts_project_id'
		AND non_unique = 0 AND sub_part IS NULL AND index_type = 'BTREE' AND is_visible = 'YES'
		AND ((seq_in_index = 1 AND column_name = 'project_id') OR (seq_in_index = 2 AND column_name = 'id'))`).Scan(&validIndex); err != nil {
		return err
	}
	if indexCount != 2 || validIndex != 2 {
		return fmt.Errorf("DNS 账户项目唯一索引不符合预期")
	}
	existing, err := validateMySQLDomainDNSForeignKeys(ctx, db)
	if err != nil {
		return err
	}
	for i, name := range []string{"fk_domain_dns_domain", "fk_domain_dns_account"} {
		if !existing[name] {
			if _, err := db.ExecContext(ctx, statements[i+2]); err != nil {
				return err
			}
		}
	}
	existing, err = validateMySQLDomainDNSForeignKeys(ctx, db)
	if err != nil {
		return err
	}
	if len(existing) != 2 {
		return fmt.Errorf("域名 DNS 绑定外键不完整")
	}
	_, err = db.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version)
	return err
}

func validateMySQLDomainDNSForeignKeys(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT r.constraint_name, r.delete_rule, r.update_rule,
		k.column_name, k.referenced_table_schema, k.referenced_table_name, k.referenced_column_name,
		k.ordinal_position, DATABASE()
		FROM information_schema.referential_constraints r JOIN information_schema.key_column_usage k
		ON k.constraint_schema = r.constraint_schema AND k.table_name = r.table_name AND k.constraint_name = r.constraint_name
		WHERE r.constraint_schema = DATABASE() AND r.table_name = 'domain_dns_accounts'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	positions := make(map[string]map[int]bool)
	for rows.Next() {
		var name, onDelete, onUpdate, column, schema, table, target, currentSchema string
		var position int
		if err := rows.Scan(&name, &onDelete, &onUpdate, &column, &schema, &table, &target, &position, &currentSchema); err != nil {
			return nil, err
		}
		expectedTable, expectedColumn := "domains", "domain_id"
		if name == "fk_domain_dns_account" {
			expectedTable, expectedColumn = "dns_accounts", "dns_account_id"
		} else if name != "fk_domain_dns_domain" {
			return nil, fmt.Errorf("域名 DNS 绑定包含未知外键")
		}
		validColumn := position == 1 && column == "project_id" && target == "project_id" || position == 2 && column == expectedColumn && target == "id"
		if !validColumn || table != expectedTable || schema != currentSchema || onDelete != "RESTRICT" || (onUpdate != "RESTRICT" && onUpdate != "NO ACTION") {
			return nil, fmt.Errorf("域名 DNS 绑定外键与预期不一致")
		}
		if positions[name] == nil {
			positions[name] = make(map[int]bool)
		}
		if positions[name][position] {
			return nil, fmt.Errorf("域名 DNS 绑定外键列重复")
		}
		positions[name][position] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make(map[string]bool)
	for name, columns := range positions {
		if len(columns) != 2 {
			return nil, fmt.Errorf("域名 DNS 绑定外键列不完整")
		}
		result[name] = true
	}
	return result, nil
}
