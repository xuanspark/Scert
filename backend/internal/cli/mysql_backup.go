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

// backupMySQL 将 MySQL 的基础表、数据、例程和触发器导出为可在 MySQL 5.7/8.0 导入的 SQL 文件。
func backupMySQL(ctx context.Context, cfg config.Config, output string) error {
	resolved, err := resolveMaintenanceDatabase(ctx, cfg.Database, cfg.Paths.Config)
	if err != nil {
		return err
	}
	return backupMySQLDatabase(ctx, resolved, output)
}

func backupMySQLDatabase(ctx context.Context, cfg config.DatabaseConfig, output string) error {
	db, err := database.Open(ctx, cfg)
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
	// 所有表的数据必须来自同一个可重复读事务，不能按表从连接池独立读取。
	// 元数据变更不属于 MVCC 快照，运维期间仍需禁止外部并发 DDL。
	tx, err := db.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("创建 MySQL 一致性备份事务失败：%w", err)
	}
	defer tx.Rollback()
	// 客户端取消会关闭连接，但服务端等待元数据锁时未必立即感知断开。
	// 仅限制本次备份会话的单次锁等待；不限制总导出时间，也不修改全局配置。
	if _, err := tx.ExecContext(ctx, "SET SESSION lock_wait_timeout = 5"); err != nil {
		return fmt.Errorf("设置 MySQL 备份元数据锁等待上限失败：%w", err)
	}

	if _, err := fmt.Fprintf(file, "-- Scert MySQL 数据备份\n-- 生成时间：%s\n\nSET FOREIGN_KEY_CHECKS=0;\n\n", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("写入 MySQL 备份头失败：%w", err)
	}

	rows, err := tx.QueryContext(ctx, "SELECT TABLE_NAME, ENGINE, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME")
	if err != nil {
		return fmt.Errorf("读取 MySQL 表列表失败：%w", err)
	}
	var tableNames []string
	for rows.Next() {
		var tableName, tableType string
		var engine sql.NullString
		if err := rows.Scan(&tableName, &engine, &tableType); err != nil {
			_ = rows.Close()
			return fmt.Errorf("读取 MySQL 表名失败：%w", err)
		}
		if tableType != "BASE TABLE" || !engine.Valid || !strings.EqualFold(engine.String, "InnoDB") {
			_ = rows.Close()
			return fmt.Errorf("MySQL 一致性备份当前仅支持 InnoDB 基础表，不能静默导出其他对象：%s", tableName)
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
	if err := requireMySQLTriggerVisibility(ctx, tx, tableNames); err != nil {
		return err
	}
	routines, err := listMySQLRoutines(ctx, tx)
	if err != nil {
		return err
	}

	for _, tableName := range tableNames {
		if err := dumpMySQLTable(ctx, tx, file, tableName); err != nil {
			return err
		}
	}
	// 例程先于依赖它的触发器创建；例程定义不执行其中的业务语句。
	if err := dumpMySQLRoutines(ctx, tx, file, routines); err != nil {
		return err
	}
	// 必须先恢复所有基础数据，再创建触发器，避免导入时重放业务副作用。
	if err := dumpMySQLTriggers(ctx, tx, file); err != nil {
		return err
	}
	if _, err := fmt.Fprint(file, "SET FOREIGN_KEY_CHECKS=1;\n"); err != nil {
		return fmt.Errorf("写入 MySQL 备份尾失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("结束 MySQL 一致性备份事务失败：%w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步 MySQL 备份文件失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭 MySQL 备份文件失败：%w", err)
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return fmt.Errorf("设置 MySQL 备份文件权限失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, output); err != nil {
		return fmt.Errorf("保存 MySQL 备份文件失败：%w", err)
	}
	removeTemporary = false
	return nil
}

// createMySQLRestoreBackup 在恢复前保留目标数据库快照，供失败时回滚使用。
// 快照目录不会在恢复成功后自动删除，便于管理员在需要时继续排查或手动恢复。
func createMySQLRestoreBackup(ctx context.Context, cfg config.Config, connection config.DatabaseConfig) (string, error) {
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
	if err := backupMySQLDatabase(ctx, connection, rollbackPath); err != nil {
		return "", fmt.Errorf("创建 MySQL 恢复前快照失败：%w", err)
	}
	return rollbackPath, nil
}

func dumpMySQLTable(ctx context.Context, db *sql.Tx, output *os.File, tableName string) error {
	quotedTable := quoteMySQLIdentifier(tableName)
	var name, createSQL string
	if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE "+quotedTable).Scan(&name, &createSQL); err != nil {
		return fmt.Errorf("读取 MySQL 表结构失败（%s）：%w", tableName, err)
	}
	if _, err := fmt.Fprintf(output, "DROP TABLE IF EXISTS %s;\n%s;\n\n", quotedTable, createSQL); err != nil {
		return fmt.Errorf("写入 MySQL 表结构失败（%s）：%w", tableName, err)
	}

	columns, err := mysqlInsertableColumns(ctx, db, tableName)
	if err != nil {
		return err
	}
	// 显式投影可写列：SELECT * 会遗漏不可见列，并错误地包含生成列的计算结果。
	projection := quoteMySQLColumns(columns)
	if len(columns) == 0 {
		// 只有生成列的表仍有行数，使用占位读取并导出空列 INSERT 保留每一行。
		projection = "1"
	}
	rows, err := db.QueryContext(ctx, "SELECT "+projection+" FROM "+quotedTable)
	if err != nil {
		return fmt.Errorf("读取 MySQL 表数据失败（%s）：%w", tableName, err)
	}
	defer rows.Close()
	selectedColumns, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("读取 MySQL 表字段失败（%s）：%w", tableName, err)
	}
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return fmt.Errorf("读取 MySQL 表字段类型失败（%s）：%w", tableName, err)
	}
	for rows.Next() {
		values := make([]any, len(selectedColumns))
		pointers := make([]any, len(values))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return fmt.Errorf("读取 MySQL 表行失败（%s）：%w", tableName, err)
		}
		literals := make([]string, len(columns))
		for index := range columns {
			literals[index] = mysqlColumnSQLLiteral(values[index], columnTypes[index].DatabaseTypeName())
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

func mysqlInsertableColumns(ctx context.Context, db *sql.Tx, tableName string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COALESCE(GENERATION_EXPRESSION, '') = '' ORDER BY ORDINAL_POSITION", tableName)
	if err != nil {
		return nil, fmt.Errorf("读取 MySQL 可写字段失败（%s）：%w", tableName, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("读取 MySQL 可写字段名失败（%s）：%w", tableName, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 MySQL 可写字段失败（%s）：%w", tableName, err)
	}
	return columns, nil
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

func mysqlColumnSQLLiteral(value any, columnType string) string {
	// 驱动将 DECIMAL 返回为文本字节；十六进制字面量在数值列中会被当作整数。
	// 使用文本字面量保留任意精度，不经浮点转换；真正的二进制列仍按原样导出。
	if columnType == "DECIMAL" {
		switch typed := value.(type) {
		case sql.RawBytes:
			return mysqlStringLiteral(string(typed))
		case []byte:
			return mysqlStringLiteral(string(typed))
		}
	}
	return mysqlSQLLiteral(value)
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
func restoreMySQL(ctx context.Context, cfg config.DatabaseConfig, input string) error {
	script, err := os.ReadFile(input)
	if err != nil {
		return fmt.Errorf("读取 MySQL 备份文件失败：%w", err)
	}
	db, err := database.Open(ctx, cfg)
	if err != nil {
		return fmt.Errorf("打开 MySQL 恢复连接失败：%w", err)
	}
	defer db.Close()
	// 会话变量与外键开关必须留在同一条连接上；失败后返回，不切换连接重放。
	connection, err := db.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取 MySQL 恢复会话失败：%w", err)
	}
	defer connection.Close()
	statements, err := splitMySQLStatements(string(script))
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if _, err := connection.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("执行 MySQL 恢复语句失败：%w", err)
		}
	}
	return nil
}

func splitMySQLStatements(script string) ([]string, error) {
	script = strings.TrimPrefix(script, "\uFEFF")
	var statements []string
	var statement strings.Builder
	delimiter := ";"
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
		if (index == 0 || script[index-1] == '\n') && strings.TrimSpace(statement.String()) == "" {
			end := strings.IndexByte(script[index:], '\n')
			if end < 0 {
				end = len(script) - index
			}
			line := strings.TrimSpace(script[index : index+end])
			if len(line) >= len("delimiter") && strings.EqualFold(line[:len("delimiter")], "delimiter") &&
				(len(line) == len("delimiter") || line[len("delimiter")] == ' ' || line[len("delimiter")] == '\t') {
				value := strings.TrimSpace(line[len("delimiter"):])
				if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
					value = value[1 : len(value)-1]
				}
				if value == "" || strings.ContainsAny(value, " \t\r\n") || strings.ContainsRune(value, '\\') || strings.ContainsRune(value, '\'') || strings.ContainsRune(value, '"') || strings.ContainsRune(value, '`') {
					return nil, fmt.Errorf("MySQL 备份中的 DELIMITER 指令无效")
				}
				delimiter = value
				statement.Reset()
				index += end
				continue
			}
		}
		if strings.HasPrefix(script[index:], delimiter) {
			flush()
			index += len(delimiter) - 1
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
		statement.WriteByte(current)
	}
	if quote != 0 || blockComment || executableComment {
		return nil, fmt.Errorf("MySQL 备份包含未闭合的字符串、标识符或注释")
	}
	flush()
	return statements, nil
}
