package database

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// MySQL 的建表语句会单独提交，恢复时必须校验结构，不能直接吞掉同名表错误。
func applyMySQLDeploymentMigration(ctx context.Context, db *sql.DB, version int, statements []string) error {
	if len(statements) == 0 {
		return fmt.Errorf("部署迁移缺少建表语句：%d", version)
	}
	shape, err := parseMySQLDeploymentShape(statements[0])
	if err != nil {
		return err
	}
	create := strings.Replace(statements[0], "CREATE TABLE ", "CREATE TABLE IF NOT EXISTS ", 1)
	if _, err := db.ExecContext(ctx, create); err != nil {
		return fmt.Errorf("创建部署迁移表失败：%w", err)
	}
	if err := validateMySQLDeploymentShape(ctx, db, shape); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if version == 15 {
		if err := backfillMySQLTaskIdempotency(ctx, tx); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
		return fmt.Errorf("记录部署迁移版本失败：%w", err)
	}
	return tx.Commit()
}

type mysqlDeploymentColumn struct {
	Name       string
	Type       string
	Default    string
	HasDefault bool
}

type mysqlDeploymentShape struct {
	Table            string
	Collation        string
	Columns          []mysqlDeploymentColumn
	Indexes          []string
	ColumnCollations map[string]string
}

var (
	mysqlDeploymentTablePattern  = regexp.MustCompile(`^CREATE TABLE ([a-z_]+) \($`)
	mysqlDeploymentColumnPattern = regexp.MustCompile(`^([a-z_]+) (VARCHAR\([0-9]+\)|INT|TEXT|MEDIUMTEXT|DATETIME) NOT NULL(?: DEFAULT (CURRENT_TIMESTAMP|0))?( PRIMARY KEY)?$`)
	mysqlDeploymentIndexPattern  = regexp.MustCompile(`^(PRIMARY KEY|UNIQUE KEY ([a-z_]+)|INDEX ([a-z_]+)) \(([a-z_, ]+)\)$`)
	mysqlIntegerWidthPattern     = regexp.MustCompile(`^int\([0-9]+\)$`)
)

// 只解析部署与报告内嵌迁移使用的语法；出现未知定义时拒绝迁移，避免漏检结构。
func parseMySQLDeploymentShape(statement string) (mysqlDeploymentShape, error) {
	var shape mysqlDeploymentShape
	lines := strings.Split(strings.TrimSpace(statement), "\n")
	if len(lines) < 3 {
		return shape, fmt.Errorf("部署迁移表定义不完整")
	}
	match := mysqlDeploymentTablePattern.FindStringSubmatch(strings.TrimSpace(lines[0]))
	closing := strings.TrimSpace(lines[len(lines)-1])
	if closing == ") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin" {
		shape.Collation = "utf8mb4_bin"
	} else if closing != ")" {
		return shape, fmt.Errorf("迁移表结尾定义不受支持")
	}
	if len(match) != 2 {
		return shape, fmt.Errorf("部署迁移表定义不受支持")
	}
	shape.Table = match[1]
	for _, raw := range lines[1 : len(lines)-1] {
		line := strings.TrimSuffix(strings.TrimSpace(raw), ",")
		if parts := mysqlDeploymentColumnPattern.FindStringSubmatch(line); len(parts) == 5 {
			shape.Columns = append(shape.Columns, mysqlDeploymentColumn{parts[1], strings.ToLower(parts[2]), strings.ToLower(parts[3]), parts[3] != ""})
			if parts[4] != "" {
				shape.Indexes = append(shape.Indexes, "PRIMARY:0:1:"+parts[1])
			}
			continue
		}
		parts := mysqlDeploymentIndexPattern.FindStringSubmatch(line)
		if len(parts) != 5 {
			return shape, fmt.Errorf("部署迁移表包含不受支持的定义：%s", shape.Table)
		}
		name, nonUnique := "PRIMARY", 0
		if parts[2] != "" {
			name = parts[2]
		} else if parts[3] != "" {
			name, nonUnique = parts[3], 1
		}
		for index, column := range strings.Split(parts[4], ",") {
			shape.Indexes = append(shape.Indexes, fmt.Sprintf("%s:%d:%d:%s", name, nonUnique, index+1, strings.TrimSpace(column)))
		}
	}
	sort.Strings(shape.Indexes)
	return shape, nil
}

func validateMySQLDeploymentShape(ctx context.Context, db *sql.DB, expected mysqlDeploymentShape) error {
	var tableCollation, schemaCollation string
	if err := db.QueryRowContext(ctx, `SELECT t.table_collation, s.default_collation_name
		FROM information_schema.tables t JOIN information_schema.schemata s ON t.table_schema = s.schema_name
		WHERE t.table_schema = DATABASE() AND t.table_name = ?`, expected.Table).Scan(&tableCollation, &schemaCollation); err != nil {
		return err
	}
	expectedCollation := schemaCollation
	if expected.Collation != "" {
		expectedCollation = expected.Collation
	}
	if tableCollation != expectedCollation {
		return fmt.Errorf("部署迁移表排序规则与数据库默认定义不匹配：%s", expected.Table)
	}
	var engine, tableType string
	if err := db.QueryRowContext(ctx, `SELECT ENGINE, TABLE_TYPE FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = ?`, expected.Table).Scan(&engine, &tableType); err != nil {
		return fmt.Errorf("读取部署迁移表状态失败：%w", err)
	}
	if !strings.EqualFold(engine, "InnoDB") || tableType != "BASE TABLE" {
		return fmt.Errorf("部署迁移表必须是 InnoDB 基础表：%s", expected.Table)
	}
	rows, err := db.QueryContext(ctx, `SELECT column_name, column_type, column_default, is_nullable, extra, collation_name
		FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position`, expected.Table)
	if err != nil {
		return err
	}
	var actual []mysqlDeploymentColumn
	for rows.Next() {
		var column mysqlDeploymentColumn
		var defaultValue sql.NullString
		var columnCollation sql.NullString
		var nullable, extra string
		if err := rows.Scan(&column.Name, &column.Type, &defaultValue, &nullable, &extra, &columnCollation); err != nil {
			rows.Close()
			return err
		}
		column.Type = strings.ToLower(column.Type)
		if mysqlIntegerWidthPattern.MatchString(column.Type) {
			column.Type = "int"
		}
		column.Default = strings.TrimSuffix(strings.ToLower(defaultValue.String), "()")
		column.HasDefault = defaultValue.Valid
		expectedColumnCollation := tableCollation
		if override := expected.ColumnCollations[column.Name]; override != "" {
			expectedColumnCollation = override
		}
		if columnCollation.Valid && columnCollation.String != expectedColumnCollation {
			rows.Close()
			return fmt.Errorf("部署迁移表字段排序规则不匹配：%s.%s", expected.Table, column.Name)
		}
		if nullable != "NO" || (extra != "" && !(strings.EqualFold(extra, "DEFAULT_GENERATED") && column.Default == "current_timestamp")) {
			rows.Close()
			return fmt.Errorf("部署迁移表字段属性不匹配：%s.%s", expected.Table, column.Name)
		}
		actual = append(actual, column)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected.Columns) {
		return fmt.Errorf("部署迁移表字段定义不匹配：%s，拒绝跳过或覆盖现有数据", expected.Table)
	}
	rows, err = db.QueryContext(ctx, `SELECT index_name, non_unique, seq_in_index, column_name, sub_part, index_type, collation
		FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ?`, expected.Table)
	if err != nil {
		return err
	}
	var indexes []string
	for rows.Next() {
		var name, column, indexType, collation string
		var nonUnique, position int
		var prefix sql.NullInt64
		if err := rows.Scan(&name, &nonUnique, &position, &column, &prefix, &indexType, &collation); err != nil {
			rows.Close()
			return err
		}
		if prefix.Valid || indexType != "BTREE" || collation != "A" {
			rows.Close()
			return fmt.Errorf("部署迁移表索引属性不匹配：%s.%s", expected.Table, name)
		}
		indexes = append(indexes, fmt.Sprintf("%s:%d:%d:%s", name, nonUnique, position, column))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	sort.Strings(indexes)
	if !reflect.DeepEqual(indexes, expected.Indexes) {
		return fmt.Errorf("部署迁移表索引定义不匹配：%s，拒绝跳过或覆盖现有数据", expected.Table)
	}
	return nil
}

// 回填与版本标记同事务提交；已落盘的历史回填只有内容完全一致才可复用。
func backfillMySQLTaskIdempotency(ctx context.Context, tx *sql.Tx) error {
	const expected = `SELECT domain_id, idempotency_key, MIN(id) AS task_id, MIN(created_at) AS created_at
		FROM tasks WHERE idempotency_key <> '' GROUP BY domain_id, idempotency_key`
	var conflicts int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_idempotency AS actual
		LEFT JOIN (`+expected+`) AS expected ON actual.domain_id = expected.domain_id AND actual.idempotency_key = expected.idempotency_key
		WHERE expected.task_id IS NULL OR BINARY actual.task_id <> BINARY expected.task_id OR actual.created_at <> expected.created_at
		OR BINARY actual.domain_id <> BINARY expected.domain_id OR BINARY actual.idempotency_key <> BINARY expected.idempotency_key`).Scan(&conflicts); err != nil {
		return fmt.Errorf("检查任务幂等历史回填失败：%w", err)
	}
	if conflicts != 0 {
		return fmt.Errorf("任务幂等历史回填与已有数据冲突，拒绝覆盖，需核查后重试")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO task_idempotency (domain_id, idempotency_key, task_id, created_at)
		SELECT expected.domain_id, expected.idempotency_key, expected.task_id, expected.created_at FROM (`+expected+`) AS expected
		LEFT JOIN task_idempotency AS actual ON actual.domain_id = expected.domain_id AND actual.idempotency_key = expected.idempotency_key
		WHERE actual.domain_id IS NULL`)
	if err != nil {
		return fmt.Errorf("回填任务幂等历史失败：%w", err)
	}
	return nil
}
