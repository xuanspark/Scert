package cli

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
)

// backupMySQL 将 MySQL 的表结构和数据导出为可在 MySQL 5.7/8.0 导入的 SQL 文件。
func backupMySQL(ctx context.Context, cfg config.Config, output string) error {
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("打开 MySQL 备份连接失败：%w", err)
	}
	defer db.Close()

	temporaryPath := output + ".tmp"
	file, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建 MySQL 备份文件失败：%w", err)
	}
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if _, err := fmt.Fprintf(file, "-- Scert MySQL 数据备份\n-- 生成时间：%s\n\nSET FOREIGN_KEY_CHECKS=0;\n\n", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("写入 MySQL 备份头失败：%w", err)
	}

	rows, err := db.DB.QueryContext(ctx, "SHOW TABLES")
	if err != nil {
		return fmt.Errorf("读取 MySQL 表列表失败：%w", err)
	}
	var tableNames []string
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			_ = rows.Close()
			return fmt.Errorf("读取 MySQL 表名失败：%w", err)
		}
		tableNames = append(tableNames, tableName)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("遍历 MySQL 表列表失败：%w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("关闭 MySQL 表列表失败：%w", err)
	}

	for _, tableName := range tableNames {
		if err := dumpMySQLTable(ctx, db.DB, file, tableName); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(file, "SET FOREIGN_KEY_CHECKS=1;\n"); err != nil {
		return fmt.Errorf("写入 MySQL 备份尾失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭 MySQL 备份文件失败：%w", err)
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return fmt.Errorf("设置 MySQL 备份文件权限失败：%w", err)
	}
	if err := os.Rename(temporaryPath, output); err != nil {
		return fmt.Errorf("保存 MySQL 备份文件失败：%w", err)
	}
	removeTemporary = false
	return nil
}

// createMySQLRestoreBackup 在恢复前保留目标数据库快照，供失败时回滚使用。
// 快照目录不会在恢复成功后自动删除，便于管理员在需要时继续排查或手动恢复。
func createMySQLRestoreBackup(ctx context.Context, cfg config.Config) (string, error) {
	backupRoot := filepath.Clean(strings.TrimSpace(cfg.Paths.Backup))
	if backupRoot == "." || backupRoot == "" {
		return "", fmt.Errorf("MySQL 恢复前备份目录不能为空")
	}
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		return "", fmt.Errorf("创建 MySQL 恢复前备份目录失败：%w", err)
	}
	info, err := os.Lstat(backupRoot)
	if err != nil {
		return "", fmt.Errorf("检查 MySQL 恢复前备份目录失败：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("MySQL 恢复前备份目录必须是普通目录")
	}
	if err := os.Chmod(backupRoot, 0o700); err != nil {
		return "", fmt.Errorf("设置 MySQL 恢复前备份目录权限失败：%w", err)
	}
	rollbackDirectory, err := os.MkdirTemp(backupRoot, "scert-before-restore-")
	if err != nil {
		return "", fmt.Errorf("创建 MySQL 恢复快照目录失败：%w", err)
	}
	rollbackPath := filepath.Join(rollbackDirectory, "scert.sql")
	if err := backupMySQL(ctx, cfg, rollbackPath); err != nil {
		return "", fmt.Errorf("创建 MySQL 恢复前快照失败：%w", err)
	}
	return rollbackPath, nil
}

func dumpMySQLTable(ctx context.Context, db *sql.DB, output *os.File, tableName string) error {
	quotedTable := quoteMySQLIdentifier(tableName)
	var name, createSQL string
	if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE "+quotedTable).Scan(&name, &createSQL); err != nil {
		return fmt.Errorf("读取 MySQL 表结构失败（%s）：%w", tableName, err)
	}
	if _, err := fmt.Fprintf(output, "DROP TABLE IF EXISTS %s;\n%s;\n\n", quotedTable, createSQL); err != nil {
		return fmt.Errorf("写入 MySQL 表结构失败（%s）：%w", tableName, err)
	}

	rows, err := db.QueryContext(ctx, "SELECT * FROM "+quotedTable)
	if err != nil {
		return fmt.Errorf("读取 MySQL 表数据失败（%s）：%w", tableName, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("读取 MySQL 表字段失败（%s）：%w", tableName, err)
	}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return fmt.Errorf("读取 MySQL 表行失败（%s）：%w", tableName, err)
		}
		literals := make([]string, len(values))
		for index, value := range values {
			literals[index] = mysqlSQLLiteral(value)
		}
		if _, err := fmt.Fprintf(output, "INSERT INTO %s (%s) VALUES (%s);\n", quotedTable, quoteMySQLColumns(columns), strings.Join(literals, ", ")); err != nil {
			return fmt.Errorf("写入 MySQL 表数据失败（%s）：%w", tableName, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 MySQL 表数据失败（%s）：%w", tableName, err)
	}
	_, err = fmt.Fprintln(output)
	return err
}

func quoteMySQLIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func quoteMySQLColumns(columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = quoteMySQLIdentifier(column)
	}
	return strings.Join(quoted, ", ")
}

func mysqlSQLLiteral(value any) string {
	switch typed := value.(type) {
	case nil:
		return "NULL"
	case sql.RawBytes:
		return "X'" + hex.EncodeToString(typed) + "'"
	case []byte:
		return "X'" + hex.EncodeToString(typed) + "'"
	case string:
		return mysqlStringLiteral(typed)
	case time.Time:
		return "'" + typed.UTC().Format("2006-01-02 15:04:05.999999") + "'"
	case bool:
		if typed {
			return "1"
		}
		return "0"
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32)
	default:
		return mysqlStringLiteral(fmt.Sprint(typed))
	}
}

func mysqlStringLiteral(value string) string {
	var builder strings.Builder
	builder.Grow(len(value) + 2)
	builder.WriteByte('\'')
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '\\':
			builder.WriteString(`\\`)
		case '\x00':
			builder.WriteString(`\0`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\x1a':
			builder.WriteString(`\Z`)
		case '\'':
			builder.WriteString(`''`)
		default:
			builder.WriteByte(value[index])
		}
	}
	builder.WriteByte('\'')
	return builder.String()
}

// restoreMySQL 执行 Scert 生成的 MySQL SQL 备份。
func restoreMySQL(ctx context.Context, cfg config.Config, input string) error {
	script, err := os.ReadFile(input)
	if err != nil {
		return fmt.Errorf("读取 MySQL 备份文件失败：%w", err)
	}
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("打开 MySQL 恢复连接失败：%w", err)
	}
	defer db.Close()
	for _, statement := range splitMySQLStatements(string(script)) {
		if _, err := db.DB.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("执行 MySQL 恢复语句失败：%w", err)
		}
	}
	return nil
}

func splitMySQLStatements(script string) []string {
	var statements []string
	var statement strings.Builder
	var quote byte
	lineComment := false
	blockComment := false
	executableComment := false
	escaped := false
	flush := func() {
		value := strings.TrimSpace(statement.String())
		if value != "" {
			statements = append(statements, value)
		}
		statement.Reset()
	}
	for index := 0; index < len(script); index++ {
		current := script[index]
		if lineComment {
			if current == '\n' {
				lineComment = false
				statement.WriteByte('\n')
			}
			continue
		}
		if blockComment {
			if current == '*' && index+1 < len(script) && script[index+1] == '/' {
				blockComment = false
				index++
				statement.WriteByte(' ')
			}
			continue
		}
		if executableComment {
			statement.WriteByte(current)
			if current == '*' && index+1 < len(script) && script[index+1] == '/' {
				statement.WriteByte(script[index+1])
				index++
				executableComment = false
			}
			continue
		}
		if quote != 0 {
			statement.WriteByte(current)
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				if index+1 < len(script) && script[index+1] == quote {
					statement.WriteByte(script[index+1])
					index++
					continue
				}
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			statement.WriteByte(current)
			continue
		}
		if current == '#' {
			lineComment = true
			continue
		}
		if current == '-' && index+2 < len(script) && script[index+1] == '-' && (script[index+2] == ' ' || script[index+2] == '\t' || script[index+2] == '\n') {
			lineComment = true
			index++
			continue
		}
		if current == '/' && index+1 < len(script) && script[index+1] == '*' {
			if index+2 < len(script) && (script[index+2] == '!' || script[index+2] == '+') {
				statement.WriteByte(current)
				statement.WriteByte(script[index+1])
				index++
				executableComment = true
				continue
			}
			blockComment = true
			index++
			continue
		}
		if current == ';' {
			flush()
			continue
		}
		statement.WriteByte(current)
	}
	flush()
	return statements
}
