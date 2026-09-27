package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

// 调用方必须持有服务维护锁，并停止所有外部数据库访问。
// 不能直接替换主文件后再处理旧 WAL，否则旧事务会覆盖恢复的数据。
func prepareSQLiteRestoreTarget(ctx context.Context, path string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, statErr := os.Lstat(path)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("检查 SQLite 恢复目标失败：%w", statErr)
	}
	if exists && !info.Mode().IsRegular() {
		return errors.New("SQLite 恢复目标必须是普通文件")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		sidecar, sidecarErr := os.Lstat(path + suffix)
		if errors.Is(sidecarErr, os.ErrNotExist) {
			continue
		}
		if sidecarErr != nil {
			return fmt.Errorf("检查 SQLite 日志文件失败：%w", sidecarErr)
		}
		if !exists || !sidecar.Mode().IsRegular() {
			return fmt.Errorf("SQLite 日志状态不安全，拒绝恢复：%s", suffix)
		}
	}
	if !exists {
		return nil
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("打开 SQLite 恢复目标失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	defer func() { err = errors.Join(err, db.Close()) }()
	// 维护窗口仍有外部事务时立即失败，不等待后继续替换文件。
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 0"); err != nil {
		return fmt.Errorf("配置 SQLite 恢复检查失败：%w", err)
	}
	var busy, logged, checkpointed int
	if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logged, &checkpointed); err != nil {
		return fmt.Errorf("合并 SQLite 恢复前日志失败：%w", err)
	}
	if busy != 0 || logged != checkpointed {
		return errors.New("SQLite 仍被其他连接占用，拒绝恢复；请停止所有数据库访问")
	}
	// 由 SQLite 自身完成恢复及日志模式切换，不手动删除日志文件。
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = DELETE").Scan(&mode); err != nil {
		return fmt.Errorf("关闭 SQLite 恢复前日志失败，请停止所有数据库访问：%w", err)
	}
	if !strings.EqualFold(mode, "delete") {
		return fmt.Errorf("SQLite 恢复前日志模式未切换完成：%s", mode)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("关闭 SQLite 恢复目标失败：%w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			if err != nil {
				return fmt.Errorf("复核 SQLite 日志文件失败：%w", err)
			}
			return fmt.Errorf("SQLite 日志仍存在，拒绝替换数据库：%s", suffix)
		}
	}
	return ctx.Err()
}
