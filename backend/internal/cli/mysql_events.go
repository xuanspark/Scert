package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

func listMySQLEvents(ctx context.Context, tx *sql.Tx) ([]string, error) {
	if err := requireMySQLEventVisibility(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT EVENT_NAME FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() ORDER BY EVENT_NAME")
	if err != nil {
		return nil, fmt.Errorf("读取 MySQL 事件列表失败：%w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func dumpMySQLEvents(ctx context.Context, tx *sql.Tx, output io.Writer, names []string) error {
	if len(names) == 0 {
		return nil
	}
	var databaseCollation string
	if err := tx.QueryRowContext(ctx, "SELECT @@collation_database").Scan(&databaseCollation); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, `SET @scert_event_sql_mode = @@SESSION.sql_mode;
SET @scert_event_charset = @@SESSION.character_set_client;
SET @scert_event_collation = @@SESSION.collation_connection;
SET @scert_event_time_zone = @@SESSION.time_zone;`); err != nil {
		return err
	}
	for _, name := range names {
		var expired sql.NullBool
		var status, completion string
		query := `SELECT CASE
		 WHEN EXECUTE_AT IS NOT NULL THEN EXECUTE_AT <= CONVERT_TZ(UTC_TIMESTAMP(), '+00:00', TIME_ZONE)
		 WHEN ENDS IS NOT NULL THEN ENDS <= CONVERT_TZ(UTC_TIMESTAMP(), '+00:00', TIME_ZONE)
		 ELSE FALSE END, STATUS, ON_COMPLETION
		 FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() AND EVENT_NAME = ?`
		if err := tx.QueryRowContext(ctx, query, name).Scan(&expired, &status, &completion); err != nil {
			return fmt.Errorf("读取 MySQL 事件到期状态失败：%w", err)
		}
		if !expired.Valid || (expired.Bool && (status == "ENABLED" || completion == "NOT PRESERVE")) {
			return fmt.Errorf("MySQL 事件已到期或时区不可验证，重新创建将改变状态或删除对象，拒绝生成不完整备份：%s", name)
		}
		definition, err := readMySQLStoredDefinition(ctx, tx, "EVENT", name, "Create Event")
		if err != nil {
			return err
		}
		zone, exists := definition["time_zone"]
		if !exists || zone == "" {
			return fmt.Errorf("MySQL 事件时区字段不完整：%s", name)
		}
		if err := normalizeMySQLEventMetadata(ctx, tx, name, definition); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "SET SESSION time_zone = %s;\n", mysqlStringLiteral(zone)); err != nil {
			return err
		}
		drop := "DROP EVENT IF EXISTS " + quoteMySQLIdentifier(name)
		if err := writeMySQLStoredDefinition(ctx, tx, output, definition, "Create Event", drop); err != nil {
			return fmt.Errorf("写入 MySQL 事件失败（%s）：%w", name, err)
		}
	}
	_, err := fmt.Fprintf(output, "ALTER DATABASE COLLATE %s;\nSET SESSION sql_mode = @scert_event_sql_mode;\nSET SESSION character_set_client = @scert_event_charset;\nSET SESSION collation_connection = @scert_event_collation;\nSET SESSION time_zone = @scert_event_time_zone;\n\n", quoteMySQLIdentifier(databaseCollation))
	return err
}

// 两个版本的事件声明头均来自 UTF-8 目录，正文仍使用原客户端编码。
// 先与目录正文比对边界，再单独反解声明头；失败不写回部分转换结果。
func normalizeMySQLEventMetadata(ctx context.Context, tx *sql.Tx, name string, definition map[string]string) error {
	charset := definition["character_set_client"]
	if charset == "utf8" || charset == "utf8mb3" || charset == "utf8mb4" {
		return nil
	}
	var body string
	if err := tx.QueryRowContext(ctx, "SELECT EVENT_DEFINITION FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() AND EVENT_NAME = ?", name).Scan(&body); err != nil {
		return fmt.Errorf("读取 MySQL 事件编码元数据失败：%w", err)
	}
	original := definition["Create Event"]
	if body == "" || !strings.HasSuffix(original, body) {
		return fmt.Errorf("MySQL 事件正文与目录元数据不一致，拒绝猜测编码：%s", name)
	}
	header := original[:len(original)-len(body)]
	if !strings.HasSuffix(header, " DO ") {
		return fmt.Errorf("MySQL 事件声明头不完整：%s", name)
	}
	identifier := quoteMySQLIdentifier(charset)
	var encoded, roundTrip []byte
	query := "SELECT CAST(CONVERT(? USING " + identifier + ") AS BINARY), CAST(CONVERT(CONVERT(? USING " + identifier + ") USING utf8mb4) AS BINARY)"
	if err := tx.QueryRowContext(ctx, query, header, header).Scan(&encoded, &roundTrip); err != nil {
		return fmt.Errorf("MySQL 事件目录元数据无法无损还原为 UTF-8（%s）：%w", name, err)
	}
	if string(roundTrip) != header || !utf8.Valid(encoded) {
		return fmt.Errorf("MySQL 事件目录元数据无法无损还原为 UTF-8：%s", name)
	}
	definition["Create Event"] = string(encoded) + body
	return nil
}
