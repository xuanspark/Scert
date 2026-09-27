package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// 固定物理连接后按 SQLite 表重建流程处理迁移，外键设置不能泄漏到连接池。
func applySQLiteTaskMigration(ctx context.Context, db *sql.DB, statements []string, version int) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取 SQLite 迁移连接失败：%w", err)
	}
	defer conn.Close()
	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("读取 SQLite 外键设置失败：%w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, resetErr := conn.ExecContext(cleanup, fmt.Sprintf("PRAGMA foreign_keys = %d", foreignKeys))
		if resetErr != nil {
			// 不能把外键状态未知的连接继续交给后续业务使用。
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			err = errors.Join(err, fmt.Errorf("恢复 SQLite 外键设置失败：%w", resetErr))
		}
	}()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("设置 SQLite 迁移连接失败：%w", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始 SQLite 迁移事务失败：%w", err)
	}
	defer tx.Rollback()
	for _, statement := range statements {
		if statement == "ALTER TABLE tasks ADD COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP" {
			err = rebuildSQLiteTasks(ctx, tx)
		} else {
			_, err = tx.ExecContext(ctx, statement)
		}
		if err != nil {
			return fmt.Errorf("执行 SQLite 任务迁移失败：%w", err)
		}
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("检查 SQLite 迁移外键失败：%w", err)
	}
	invalid := rows.Next()
	rowErr := errors.Join(rows.Err(), rows.Close())
	if rowErr != nil {
		return fmt.Errorf("读取 SQLite 迁移外键结果失败：%w", rowErr)
	}
	if invalid {
		return errors.New("SQLite 任务迁移存在外键冲突，已拒绝提交")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
		return fmt.Errorf("记录 SQLite 迁移版本失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 SQLite 迁移事务失败：%w", err)
	}
	return nil
}

var sqliteTaskTableHeader = regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:tasks|"tasks"|` + "`tasks`" + `|\[tasks\])\s*\(`)

type sqliteTaskSchemaObject struct {
	kind string
	name string
	sql  string
}

func rebuildSQLiteTasks(ctx context.Context, tx *sql.Tx) error {
	var definition string
	if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'tasks'").Scan(&definition); err != nil {
		return err
	}
	header := sqliteTaskTableHeader.FindStringIndex(definition)
	end := strings.LastIndex(definition, ")")
	if header == nil || end < header[1] {
		return errors.New("无法安全识别 SQLite 任务表定义，拒绝重建")
	}
	// 保留原始列、约束和表选项，不用硬编码的旧表字段覆盖扩展结构。
	create := `CREATE TABLE "scert_tasks_migration_4" (` + definition[header[1]:end] + ", updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP" + definition[end:]
	columns, err := sqliteTaskCopyColumns(ctx, tx)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT type, name, sql FROM sqlite_master WHERE sql IS NOT NULL AND (type IN ('view', 'trigger') OR (type = 'index' AND tbl_name = 'tasks')) ORDER BY type, name`)
	if err != nil {
		return err
	}
	var objects []sqliteTaskSchemaObject
	for rows.Next() {
		var object sqliteTaskSchemaObject
		if err := rows.Scan(&object.kind, &object.name, &object.sql); err != nil {
			_ = rows.Close()
			return err
		}
		objects = append(objects, object)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	// 名字冲突时直接失败，不覆盖或删除已有对象。
	if _, err := tx.ExecContext(ctx, create); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO "scert_tasks_migration_4" (`+columns+`) SELECT `+columns+` FROM tasks`); err != nil {
		return err
	}
	// 重命名会校验整个模式，临时移除依赖视图和触发器后按原定义恢复。
	for _, object := range objects {
		if object.kind == "view" || object.kind == "trigger" {
			if _, err := tx.ExecContext(ctx, "DROP "+strings.ToUpper(object.kind)+" "+quoteSQLiteMigrationName(object.name)); err != nil {
				return err
			}
		}
	}
	for _, statement := range []string{"DROP TABLE tasks", `ALTER TABLE "scert_tasks_migration_4" RENAME TO tasks`} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	// 先恢复视图，最后恢复触发器，兼容视图上的 INSTEAD OF 触发器。
	for _, kind := range []string{"index", "view", "trigger"} {
		for _, object := range objects {
			if object.kind == kind {
				if _, err := tx.ExecContext(ctx, object.sql); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func sqliteTaskCopyColumns(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT name FROM pragma_table_xinfo('tasks') WHERE hidden = 0 ORDER BY cid")
	if err != nil {
		return "", err
	}
	var columns []string
	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return "", err
		}
		names[strings.ToLower(name)] = true
		columns = append(columns, quoteSQLiteMigrationName(name))
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", err
	}
	var withoutRowID int
	if err := tx.QueryRowContext(ctx, "SELECT wr FROM pragma_table_list WHERE schema = 'main' AND name = 'tasks'").Scan(&withoutRowID); err != nil {
		return "", err
	}
	if withoutRowID == 0 {
		for _, alias := range []string{"rowid", "_rowid_", "oid"} {
			if !names[alias] {
				columns = append([]string{quoteSQLiteMigrationName(alias)}, columns...)
				break
			}
		}
	}
	return strings.Join(columns, ","), nil
}

func quoteSQLiteMigrationName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
