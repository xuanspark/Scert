package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
)

func dumpMySQLTriggers(ctx context.Context, tx *sql.Tx, output io.Writer) error {
	// 名称排序不能代表执行顺序；同一表、时机和事件按服务器的 ACTION_ORDER 导出。
	rows, err := tx.QueryContext(ctx, `SELECT TRIGGER_NAME FROM information_schema.TRIGGERS
	 WHERE TRIGGER_SCHEMA = DATABASE()
	 ORDER BY EVENT_OBJECT_TABLE, ACTION_TIMING, EVENT_MANIPULATION, ACTION_ORDER`)
	if err != nil {
		return fmt.Errorf("读取 MySQL 触发器列表失败：%w", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("读取 MySQL 触发器名称失败：%w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("遍历 MySQL 触发器失败：%w", err)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	var databaseCollation string
	if err := tx.QueryRowContext(ctx, "SELECT @@collation_database").Scan(&databaseCollation); err != nil {
		return fmt.Errorf("读取 MySQL 数据库排序规则失败：%w", err)
	}
	if _, err := fmt.Fprintln(output, `SET @scert_trigger_sql_mode = @@SESSION.sql_mode;
SET @scert_trigger_charset = @@SESSION.character_set_client;
SET @scert_trigger_collation = @@SESSION.collation_connection;`); err != nil {
		return err
	}
	for _, name := range names {
		definition, err := readMySQLTrigger(ctx, tx, name)
		if err != nil {
			return err
		}
		// 文件中的创建语句使用原客户端字符集，恢复时才能同时保留正文和创建元数据。
		var encoded, roundTrip []byte
		charset := quoteMySQLIdentifier(definition["character_set_client"])
		query := "SELECT CAST(CONVERT(? USING " + charset + ") AS BINARY), CAST(CONVERT(CONVERT(? USING " + charset + ") USING utf8mb4) AS BINARY)"
		original := definition["SQL Original Statement"]
		if err := tx.QueryRowContext(ctx, query, original, original).Scan(&encoded, &roundTrip); err != nil {
			return fmt.Errorf("转换 MySQL 触发器字符集失败（%s）：%w", name, err)
		}
		if string(roundTrip) != original {
			return fmt.Errorf("MySQL 触发器无法无损转换到创建字符集（%s）", name)
		}
		delimiter := ";;"
		for strings.Contains(string(encoded), delimiter) {
			delimiter += ";"
		}
		_, err = fmt.Fprintf(output, "ALTER DATABASE COLLATE %s;\nSET SESSION sql_mode = %s;\nSET SESSION character_set_client = %s;\nSET SESSION collation_connection = %s;\nDELIMITER %s\n%s%s\nDELIMITER ;\n",
			quoteMySQLIdentifier(definition["Database Collation"]), mysqlStringLiteral(definition["sql_mode"]),
			mysqlStringLiteral(definition["character_set_client"]), mysqlStringLiteral(definition["collation_connection"]), delimiter, encoded, delimiter)
		if err != nil {
			return fmt.Errorf("写入 MySQL 触发器失败（%s）：%w", name, err)
		}
	}
	_, err = fmt.Fprintf(output, "ALTER DATABASE COLLATE %s;\nSET SESSION sql_mode = @scert_trigger_sql_mode;\nSET SESSION character_set_client = @scert_trigger_charset;\nSET SESSION collation_connection = @scert_trigger_collation;\n\n", quoteMySQLIdentifier(databaseCollation))
	return err
}

func readMySQLTrigger(ctx context.Context, tx *sql.Tx, name string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, "SHOW CREATE TRIGGER "+quoteMySQLIdentifier(name))
	if err != nil {
		return nil, fmt.Errorf("读取 MySQL 触发器定义失败（%s）：%w", name, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]sql.NullString, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("MySQL 触发器定义不可见（%s）", name)
	}
	if err := rows.Scan(pointers...); err != nil {
		return nil, err
	}
	definition := make(map[string]string, len(columns))
	for i, column := range columns {
		if values[i].Valid {
			definition[column] = values[i].String
		}
	}
	for _, column := range []string{"sql_mode", "SQL Original Statement", "character_set_client", "collation_connection", "Database Collation"} {
		value, exists := definition[column]
		if !exists || (value == "" && column != "sql_mode") {
			return nil, fmt.Errorf("MySQL 触发器定义字段不完整（%s）：%s", name, column)
		}
	}
	if rows.Next() {
		return nil, fmt.Errorf("MySQL 触发器定义返回了多行（%s）", name)
	}
	return definition, rows.Err()
}
