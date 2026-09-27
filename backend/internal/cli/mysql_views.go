package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
)

type mysqlView struct {
	name       string
	definition map[string]string
}

func dumpMySQLViews(ctx context.Context, tx *sql.Tx, output io.Writer, names []string) (result error) {
	if len(names) == 0 {
		return nil
	}
	var schema, databaseCollation, sourceMode string
	var lowerCaseNames, sourceQuote int
	if err := tx.QueryRowContext(ctx, "SELECT DATABASE(), @@collation_database, @@SESSION.sql_mode, @@lower_case_table_names, @@SESSION.sql_quote_show_create").Scan(&schema, &databaseCollation, &sourceMode, &lowerCaseNames, &sourceQuote); err != nil {
		return err
	}
	// 仅解析服务端在固定模式下输出的依赖；创建语句保持原样，不重写用户 SQL。
	if _, err := tx.ExecContext(ctx, "SET SESSION sql_mode = '', SESSION sql_quote_show_create = 1"); err != nil {
		return err
	}
	defer func() {
		_, err := tx.ExecContext(ctx, "SET SESSION sql_mode = ?, SESSION sql_quote_show_create = ?", sourceMode, sourceQuote)
		result = errors.Join(result, err)
	}()
	views := make([]mysqlView, 0, len(names))
	for _, name := range names {
		definition, err := readMySQLStoredDefinition(ctx, tx, "VIEW", name, "Create View")
		if err != nil {
			return err
		}
		definition["sql_mode"] = ""
		definition["Database Collation"] = databaseCollation
		var columns int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", name).Scan(&columns); err != nil {
			return fmt.Errorf("读取 MySQL 视图列失败（%s）：%w", name, err)
		}
		if columns == 0 {
			return fmt.Errorf("MySQL 视图列不可见或已失效，拒绝生成不完整备份：%s", name)
		}
		views = append(views, mysqlView{name: name, definition: definition})
	}
	views, err := orderMySQLViews(views, schema, lowerCaseNames)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, `SET @scert_view_sql_mode = @@SESSION.sql_mode;
SET @scert_view_charset = @@SESSION.character_set_client;
SET @scert_view_collation = @@SESSION.collation_connection;
SET SESSION sql_mode = '';
SET SESSION character_set_client = 'utf8mb4';
SET SESSION collation_connection = 'utf8mb4_bin';`); err != nil {
		return err
	}
	// 先按反向依赖删除视图，再正向创建；不创建占位表，也绝不删除同名基础表。
	for i := len(views) - 1; i >= 0; i-- {
		if _, err := fmt.Fprintf(output, "DROP VIEW IF EXISTS %s;\n", quoteMySQLIdentifier(views[i].name)); err != nil {
			return err
		}
	}
	for _, view := range views {
		if err := writeMySQLStoredDefinition(ctx, tx, output, view.definition, "Create View", ""); err != nil {
			return fmt.Errorf("写入 MySQL 视图失败（%s）：%w", view.name, err)
		}
	}
	_, err = fmt.Fprintln(output, `SET SESSION sql_mode = @scert_view_sql_mode;
SET SESSION character_set_client = @scert_view_charset;
SET SESSION collation_connection = @scert_view_collation;`)
	return err
}
