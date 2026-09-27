package database

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
)

// 保留锁定驱动的原有接口，不通过重放业务语句实现锁等待或超时。
type sqliteDriverConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Execer
	driver.Queryer
	driver.Pinger
	driver.SessionResetter
	driver.Validator
	Serialize() ([]byte, error)
	Deserialize([]byte) error
	NewBackup(string) (*sqlite.Backup, error)
	NewRestore(string) (*sqlite.Backup, error)
}

type sqliteDeadlineConn struct {
	sqliteDriverConn
	invalid bool
}

func (c *sqliteDeadlineConn) IsValid() bool {
	return !c.invalid && c.sqliteDriverConn.IsValid()
}

func (c *sqliteDeadlineConn) ResetSession(ctx context.Context) error {
	if c.invalid {
		return driver.ErrBadConn
	}
	return c.sqliteDriverConn.ResetSession(ctx)
}

// SQLite 原生忙等待可能晚于上下文截止时间；缩短本次调用的等待预算，结束后恢复。
// 不修改没有截止时间的调用；手动取消仍依赖原生驱动中断，不能承诺立即退出忙等待。
func sqliteWithDeadline[T any](c *sqliteDeadlineConn, ctx context.Context, run func() (T, error)) (result T, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	defer func() {
		var sqliteErr *sqlite.Error
		if ctx.Err() != nil && errors.As(err, &sqliteErr) && (sqliteErr.Code()&255 == 5 || sqliteErr.Code()&255 == 9) {
			// 原生 BEGIN 不一定转换上下文错误；保留 SQLite 错误供调用者诊断。
			err = errors.Join(ctx.Err(), err)
		}
	}()
	deadline, limited := ctx.Deadline()
	remaining := time.Until(deadline)
	if !limited || remaining >= 5*time.Second {
		return run()
	}
	if remaining <= 0 {
		return result, context.DeadlineExceeded
	}
	// 向上取整，避免在截止时间之前以 SQLITE_BUSY 提前返回。
	millis := (remaining + time.Millisecond - 1) / time.Millisecond
	if _, err := c.sqliteDriverConn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", millis), nil); err != nil {
		c.invalid = true
		return result, fmt.Errorf("配置 SQLite 截止时间等待预算失败：%w", err)
	}
	defer func() {
		if _, restoreErr := c.sqliteDriverConn.ExecContext(context.Background(), "PRAGMA busy_timeout = 5000", nil); restoreErr != nil {
			c.invalid = true
			// 执行后的清理错误不能转换成 ErrBadConn，避免数据库层自动重试已执行语句。
			err = errors.Join(err, fmt.Errorf("恢复 SQLite 忙等待配置失败：%w", restoreErr))
		}
	}()
	result, err = run()
	return result, err
}

func (c *sqliteDeadlineConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return sqliteWithDeadline(c, ctx, func() (driver.Result, error) {
		return c.sqliteDriverConn.ExecContext(ctx, query, args)
	})
}

func (c *sqliteDeadlineConn) Ping(ctx context.Context) error {
	_, err := c.ExecContext(ctx, "SELECT 1", nil)
	return err
}

func (c *sqliteDeadlineConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := sqliteWithDeadline(c, ctx, func() (driver.Rows, error) {
		return c.sqliteDriverConn.QueryContext(ctx, query, args)
	})
	return c.wrapRows(ctx, rows, err)
}

func (c *sqliteDeadlineConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return sqliteWithDeadline(c, ctx, func() (driver.Tx, error) {
		return c.sqliteDriverConn.BeginTx(ctx, options)
	})
}

func (c *sqliteDeadlineConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stmt, err := c.sqliteDriverConn.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	full, ok := stmt.(sqliteDriverStmt)
	if !ok {
		_ = stmt.Close()
		return nil, fmt.Errorf("SQLite 驱动预处理接口不兼容")
	}
	return &sqliteDeadlineStmt{sqliteDriverStmt: full, conn: c}, nil
}

func (c *sqliteDeadlineConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

type sqliteDriverStmt interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}

type sqliteDeadlineStmt struct {
	sqliteDriverStmt
	conn *sqliteDeadlineConn
}

func (s *sqliteDeadlineStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return sqliteWithDeadline(s.conn, ctx, func() (driver.Result, error) {
		return s.sqliteDriverStmt.ExecContext(ctx, args)
	})
}

func (s *sqliteDeadlineStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := sqliteWithDeadline(s.conn, ctx, func() (driver.Rows, error) {
		return s.sqliteDriverStmt.QueryContext(ctx, args)
	})
	return s.conn.wrapRows(ctx, rows, err)
}

var _ sqliteDriverConn = (*sqliteDeadlineConn)(nil)
var _ sqliteDriverStmt = (*sqliteDeadlineStmt)(nil)
