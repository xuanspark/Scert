package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
)

type mysqlRoutine struct {
	name               string
	kind               string
	utf8MetadataHeader bool
}

func listMySQLRoutines(ctx context.Context, tx *sql.Tx) ([]mysqlRoutine, error) {
	var name, version string
	var lowerCaseNames int
	if err := tx.QueryRowContext(ctx, "SELECT DATABASE(), VERSION(), @@lower_case_table_names").Scan(&name, &version, &lowerCaseNames); err != nil {
		return nil, fmt.Errorf("读取 MySQL 例程权限环境失败：%w", err)
	}
	query := "SELECT ROUTINE_NAME, ROUTINE_TYPE FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() ORDER BY ROUTINE_TYPE, ROUTINE_NAME"
	switch {
	case strings.HasPrefix(version, "5.7."):
		// 直接证明系统目录的完整读取权限，不能依赖会静默隐藏他人例程的元数据列表。
		rows, err := tx.QueryContext(ctx, "SELECT * FROM mysql.proc LIMIT 0")
		if err != nil {
			return nil, fmt.Errorf("MySQL 例程备份需要 mysql.proc 的完整 SELECT 权限：%w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		query = "SELECT name, type FROM mysql.proc WHERE db = DATABASE() ORDER BY type, name"
	case strings.HasPrefix(version, "8.0."):
		grants, err := readMySQLEffectiveGrants(ctx, tx, version)
		if err != nil {
			return nil, err
		}
		if !mysqlRoutineVisibilityGranted(grants, name, lowerCaseNames != 0) {
			return nil, fmt.Errorf("MySQL 例程备份需要有效 SHOW_ROUTINE 或未被撤销的全局 SELECT 权限，不能把隐藏的例程当成不存在")
		}
	default:
		return nil, fmt.Errorf("当前 MySQL 版本的完整例程可见权限尚未验证：%s", version)
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("读取 MySQL 例程列表失败：%w", err)
	}
	defer rows.Close()
	var routines []mysqlRoutine
	for rows.Next() {
		var routine mysqlRoutine
		if err := rows.Scan(&routine.name, &routine.kind); err != nil {
			return nil, err
		}
		if routine.kind != "PROCEDURE" && routine.kind != "FUNCTION" {
			return nil, fmt.Errorf("MySQL 例程类型不受支持：%s", routine.kind)
		}
		routine.utf8MetadataHeader = strings.HasPrefix(version, "8.0.")
		routines = append(routines, routine)
	}
	return routines, rows.Err()
}

// SHOW_ROUTINE 是独立动态权限；库级 EXECUTE 或 CREATE ROUTINE 不能证明所有定义都可见。
func mysqlRoutineVisibilityGranted(grants []string, name string, foldCase bool) bool {
	showRoutine, globalSelect, revoked := false, false, false
	for _, grant := range grants {
		match := mysqlPrivilegeGrant.FindStringSubmatch(grant)
		if len(match) == 0 {
			continue
		}
		selectPrivilege, showPrivilege := false, false
		for _, privilege := range strings.Split(match[2], ",") {
			switch strings.TrimSpace(privilege) {
			case "SELECT", "ALL PRIVILEGES":
				selectPrivilege = true
			case "SHOW_ROUTINE":
				showPrivilege = true
			}
		}
		if match[1] == "GRANT" && match[3] == "*" && match[4] == "*" {
			showRoutine = showRoutine || showPrivilege
			globalSelect = globalSelect || selectPrivilege
		}
		schema := unquoteMySQLGrantIdentifier(match[3])
		sameSchema := schema == name || (foldCase && strings.EqualFold(schema, name))
		if match[1] == "REVOKE" && sameSchema && match[4] == "*" {
			revoked = revoked || selectPrivilege
		}
	}
	return showRoutine || (globalSelect && !revoked)
}

func dumpMySQLRoutines(ctx context.Context, tx *sql.Tx, output io.Writer, routines []mysqlRoutine) error {
	if len(routines) == 0 {
		return nil
	}
	var databaseCollation string
	if err := tx.QueryRowContext(ctx, "SELECT @@collation_database").Scan(&databaseCollation); err != nil {
		return fmt.Errorf("读取 MySQL 数据库排序规则失败：%w", err)
	}
	if _, err := fmt.Fprintln(output, `SET @scert_routine_sql_mode = @@SESSION.sql_mode;
SET @scert_routine_charset = @@SESSION.character_set_client;
SET @scert_routine_collation = @@SESSION.collation_connection;`); err != nil {
		return err
	}
	for _, routine := range routines {
		column := "Create Procedure"
		if routine.kind == "FUNCTION" {
			column = "Create Function"
		}
		definition, err := readMySQLStoredDefinition(ctx, tx, routine.kind, routine.name, column)
		if err != nil {
			return err
		}
		if routine.utf8MetadataHeader {
			if err := normalizeMySQLRoutineMetadata(ctx, tx, routine, definition, column); err != nil {
				return err
			}
		}
		// 同名例程必须替换；定义者和权限语义来自原始 SHOW CREATE，不擅自改写。
		drop := "DROP " + routine.kind + " IF EXISTS " + quoteMySQLIdentifier(routine.name)
		if err := writeMySQLStoredDefinition(ctx, tx, output, definition, column, drop); err != nil {
			return fmt.Errorf("写入 MySQL 例程失败（%s）：%w", routine.name, err)
		}
	}
	_, err := fmt.Fprintf(output, "ALTER DATABASE COLLATE %s;\nSET SESSION sql_mode = @scert_routine_sql_mode;\nSET SESSION character_set_client = @scert_routine_charset;\nSET SESSION collation_connection = @scert_routine_collation;\n\n", quoteMySQLIdentifier(databaseCollation))
	return err
}
