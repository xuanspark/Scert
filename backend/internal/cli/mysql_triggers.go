package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
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
		if err := writeMySQLStoredDefinition(ctx, tx, output, definition, "SQL Original Statement", ""); err != nil {
			return fmt.Errorf("写入 MySQL 触发器失败（%s）：%w", name, err)
		}
	}
	_, err = fmt.Fprintf(output, "ALTER DATABASE COLLATE %s;\nSET SESSION sql_mode = @scert_trigger_sql_mode;\nSET SESSION character_set_client = @scert_trigger_charset;\nSET SESSION collation_connection = @scert_trigger_collation;\n\n", quoteMySQLIdentifier(databaseCollation))
	return err
}

func readMySQLTrigger(ctx context.Context, tx *sql.Tx, name string) (map[string]string, error) {
	return readMySQLStoredDefinition(ctx, tx, "TRIGGER", name, "SQL Original Statement")
}
