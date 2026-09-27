package cli

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

var mysqlPrivilegeGrant = regexp.MustCompile("^(GRANT|REVOKE) (.+?) ON (" + "`(?:``|[^`])*`" + "|\\*)\\.(" + "`(?:``|[^`])*`" + "|\\*) (?:TO|FROM) ")

// 元数据会按权限静默过滤；必须先证明每张可见基础表的 TRIGGER 权限，不能把空列表当成无触发器。
func requireMySQLTriggerVisibility(ctx context.Context, tx *sql.Tx, tables []string) error {
	if len(tables) == 0 {
		return nil
	}
	var name, version string
	var lowerCaseNames int
	if err := tx.QueryRowContext(ctx, "SELECT DATABASE(), VERSION(), @@lower_case_table_names").Scan(&name, &version, &lowerCaseNames); err != nil {
		return err
	}
	normalize := func(value string) string {
		if lowerCaseNames != 0 {
			return strings.ToLower(value)
		}
		return value
	}
	name = normalize(name)
	query := "SHOW GRANTS FOR CURRENT_USER"
	if strings.HasPrefix(version, "8.0.") {
		rows, err := tx.QueryContext(ctx, "SELECT ROLE_NAME, ROLE_HOST FROM information_schema.ENABLED_ROLES ORDER BY ROLE_NAME, ROLE_HOST")
		if err != nil {
			return fmt.Errorf("读取 MySQL 当前角色失败：%w", err)
		}
		var roles []string
		for rows.Next() {
			var role, host string
			if err := rows.Scan(&role, &host); err != nil {
				_ = rows.Close()
				return err
			}
			roles = append(roles, quoteMySQLIdentifier(role)+"@"+quoteMySQLIdentifier(host))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(roles) > 0 {
			query += " USING " + strings.Join(roles, ", ")
		}
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("读取 MySQL 有效权限失败：%w", err)
	}
	var grants []string
	for rows.Next() {
		var grant string
		if err := rows.Scan(&grant); err != nil {
			_ = rows.Close()
			return err
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	// 5.7 返回空列表；8.0 部分撤销模式下，数据库授权中的百分号和下划线为字面量。
	partialRevokes := false
	rows, err = tx.QueryContext(ctx, "SHOW GLOBAL VARIABLES LIKE 'partial_revokes'")
	if err != nil {
		return err
	}
	for rows.Next() {
		var variable, value string
		if err := rows.Scan(&variable, &value); err != nil {
			_ = rows.Close()
			return err
		}
		partialRevokes = strings.EqualFold(value, "ON")
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	global, revoked := false, false
	exactSeen, exactAllowed := false, false
	wildcardSeen, wildcardAllowed := false, true
	covered := make(map[string]bool)
	for _, grant := range grants {
		match := mysqlPrivilegeGrant.FindStringSubmatch(grant)
		if len(match) == 0 {
			continue
		}
		hasTrigger := mysqlHasTriggerPrivilege(match[2])
		if match[3] == "*" && match[4] == "*" {
			global = global || (match[1] == "GRANT" && hasTrigger)
			continue
		}
		databaseName := normalize(unquoteMySQLGrantIdentifier(match[3]))
		matches := name == databaseName
		if match[4] == "*" && !partialRevokes && match[1] == "GRANT" {
			if err := tx.QueryRowContext(ctx, "SELECT BINARY ? LIKE BINARY ?", name, databaseName).Scan(&matches); err != nil {
				return err
			}
		}
		if !matches {
			continue
		}
		if match[1] == "REVOKE" {
			revoked = revoked || hasTrigger
		} else if match[4] == "*" {
			if partialRevokes || !mysqlGrantHasWildcard(databaseName) {
				exactSeen = true
				exactAllowed = exactAllowed || hasTrigger
			} else {
				wildcardSeen = true
				wildcardAllowed = wildcardAllowed && hasTrigger
			}
		} else if hasTrigger {
			covered[normalize(unquoteMySQLGrantIdentifier(match[4]))] = true
		}
	}
	// 精确库授权优先。多个匹配通配授权可能按内部顺序遮蔽，不能简单合并为更大权限。
	// 不能证明所有候选都包含 TRIGGER 时保守失败，管理员可提供明确的精确库或表级授权。
	schema := exactAllowed
	if !exactSeen {
		schema = wildcardSeen && wildcardAllowed
	}
	for _, table := range tables {
		if !((global && !revoked) || schema || covered[normalize(table)]) {
			return fmt.Errorf("MySQL 备份无法确认表 %s 的 TRIGGER 可见权限，不能把隐藏的触发器当成不存在", table)
		}
	}
	return nil
}

func mysqlGrantHasWildcard(value string) bool {
	escaped := false
	for _, char := range value {
		if escaped {
			escaped = false
		} else if char == rune(92) {
			escaped = true
		} else if char == '%' || char == '_' {
			return true
		}
	}
	return false
}

func mysqlHasTriggerPrivilege(privileges string) bool {
	for _, privilege := range strings.Split(privileges, ",") {
		if value := strings.TrimSpace(privilege); value == "TRIGGER" || value == "ALL PRIVILEGES" {
			return true
		}
	}
	return false
}

func unquoteMySQLGrantIdentifier(value string) string {
	if len(value) >= 2 && value[0] == '`' && value[len(value)-1] == '`' {
		return strings.ReplaceAll(value[1:len(value)-1], "``", "`")
	}
	return value
}
