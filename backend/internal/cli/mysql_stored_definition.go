package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
)

func readMySQLStoredDefinition(ctx context.Context, tx *sql.Tx, kind, name, statementColumn string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, "SHOW CREATE "+kind+" "+quoteMySQLIdentifier(name))
	if err != nil {
		return nil, fmt.Errorf("读取 MySQL 存储对象定义失败（%s %s）：%w", kind, name, err)
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
		return nil, fmt.Errorf("MySQL 存储对象定义不可见（%s %s）", kind, name)
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
	required := []string{"sql_mode", statementColumn, "character_set_client", "collation_connection", "Database Collation"}
	if kind == "VIEW" {
		required = []string{statementColumn, "character_set_client", "collation_connection"}
	}
	for _, column := range required {
		value, exists := definition[column]
		if !exists || (value == "" && column != "sql_mode") {
			return nil, fmt.Errorf("MySQL 存储对象定义字段不完整（%s %s）：%s", kind, name, column)
		}
	}
	if rows.Next() {
		return nil, fmt.Errorf("MySQL 存储对象定义返回了多行（%s %s）", kind, name)
	}
	return definition, rows.Err()
}

func writeMySQLStoredDefinition(ctx context.Context, tx *sql.Tx, output io.Writer, definition map[string]string, column, drop string) error {
	charset := quoteMySQLIdentifier(definition["character_set_client"])
	encode := func(original string) (string, error) {
		var encoded, roundTrip []byte
		query := "SELECT CAST(CONVERT(? USING " + charset + ") AS BINARY), CAST(CONVERT(CONVERT(? USING " + charset + ") USING utf8mb4) AS BINARY)"
		if err := tx.QueryRowContext(ctx, query, original, original).Scan(&encoded, &roundTrip); err != nil {
			return "", fmt.Errorf("转换 MySQL 存储对象字符集失败：%w", err)
		}
		if string(roundTrip) != original {
			return "", fmt.Errorf("MySQL 存储对象无法无损转换到创建字符集")
		}
		return string(encoded), nil
	}
	// 删除和创建语句均使用原客户端编码，非 ASCII 标识符不能混用 UTF-8 字节。
	encoded, err := encode(definition[column])
	if err != nil {
		return err
	}
	encodedDrop := ""
	if drop != "" {
		encodedDrop, err = encode(drop)
		if err != nil {
			return err
		}
	}
	delimiter := ";;"
	for strings.Contains(encoded, delimiter) || strings.Contains(encodedDrop, delimiter) {
		delimiter += ";"
	}
	if _, err := fmt.Fprintf(output, "ALTER DATABASE COLLATE %s;\nSET SESSION sql_mode = %s;\nSET SESSION character_set_client = %s;\nSET SESSION collation_connection = %s;\nDELIMITER %s\n",
		quoteMySQLIdentifier(definition["Database Collation"]), mysqlStringLiteral(definition["sql_mode"]),
		mysqlStringLiteral(definition["character_set_client"]), mysqlStringLiteral(definition["collation_connection"]), delimiter); err != nil {
		return err
	}
	if encodedDrop != "" {
		if _, err := fmt.Fprintf(output, "%s%s\n", encodedDrop, delimiter); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(output, "%s%s\nDELIMITER ;\n", encoded, delimiter)
	return err
}
