package cli

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 两个版本的目录注释均需单独还原，8.0 还需还原声明头；
// 非 UTF-8 例程必须分段处理，不能整体反解原客户端编码的正文。
func normalizeMySQLRoutineMetadata(ctx context.Context, tx *sql.Tx, routine mysqlRoutine, definition map[string]string, column string) error {
	charset := definition["character_set_client"]
	if charset == "utf8" || charset == "utf8mb3" || charset == "utf8mb4" {
		return nil
	}
	original := definition[column]
	end, err := mysqlRoutineHeaderEnd(original)
	if err != nil {
		return err
	}
	decode := func(value string) (string, error) {
		var encoded, roundTrip []byte
		identifier := quoteMySQLIdentifier(charset)
		query := "SELECT CAST(CONVERT(? USING " + identifier + ") AS BINARY), CAST(CONVERT(CONVERT(? USING " + identifier + ") USING utf8mb4) AS BINARY)"
		if err := tx.QueryRowContext(ctx, query, value, value).Scan(&encoded, &roundTrip); err != nil {
			return "", fmt.Errorf("MySQL 例程目录元数据无法无损还原为 UTF-8（%s）：%w", routine.name, err)
		}
		if string(roundTrip) != value || !utf8.Valid(encoded) {
			return "", fmt.Errorf("MySQL 例程目录元数据无法无损还原为 UTF-8：%s", routine.name)
		}
		return string(encoded), nil
	}
	header := original[:end]
	if routine.utf8MetadataHeader {
		header, err = decode(header)
		if err != nil {
			return err
		}
	}
	var comment string
	var body sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT ROUTINE_COMMENT, ROUTINE_DEFINITION FROM information_schema.ROUTINES
	 WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_NAME = ? AND ROUTINE_TYPE = ?`, routine.name, routine.kind).Scan(&comment, &body); err != nil {
		return fmt.Errorf("读取 MySQL 例程编码元数据失败：%w", err)
	}
	rest := original[end:]
	if comment != "" {
		if !body.Valid || body.String == "" || !strings.HasSuffix(rest, body.String) {
			return fmt.Errorf("MySQL 例程正文与目录元数据不一致，拒绝猜测编码：%s", routine.name)
		}
		metadata := rest[:len(rest)-len(body.String)]
		// 注释位于声明选项的末尾；先隔离正文，避免误改业务 SQL 中的 COMMENT 文本。
		start := strings.LastIndex(metadata, "\n    COMMENT ")
		if start < 0 {
			return fmt.Errorf("MySQL 例程注释声明不完整：%s", routine.name)
		}
		decoded, err := decode(metadata[start:])
		if err != nil {
			return err
		}
		rest = metadata[:start] + decoded + body.String
	}
	definition[column] = header + rest
	return nil
}

func mysqlRoutineHeaderEnd(statement string) (int, error) {
	var quote byte
	for i := 0; i < len(statement); i++ {
		current := statement[i]
		if quote != 0 {
			if current == quote {
				if i+1 < len(statement) && statement[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if current == '`' || current == '"' || current == '\'' {
			quote = current
		} else if current == '(' {
			return i, nil
		}
	}
	return 0, fmt.Errorf("MySQL 例程声明缺少完整参数列表")
}
