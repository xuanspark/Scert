package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"modernc.org/sqlite"
)

func openSQLitePool(dsn string) *sql.DB {
	return sql.OpenDB(&sqlitePoolConnector{driver: &sqlite.Driver{}, dsn: dsn})
}

type sqlitePoolConnector struct {
	driver driver.Driver
	dsn    string
}

func (c *sqlitePoolConnector) Driver() driver.Driver {
	return c.driver
}

func (c *sqlitePoolConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	full, ok := conn.(sqliteDriverConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("SQLite 驱动连接接口不兼容")
	}
	wrapped := &sqliteDeadlineConn{sqliteDriverConn: full}
	if err := configureSQLiteConnection(ctx, wrapped); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return wrapped, nil
}

func configureSQLiteConnection(ctx context.Context, conn driver.Conn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full, ok := conn.(sqlite.ExecQuerierContext)
	if !ok {
		return fmt.Errorf("SQLite 驱动连接接口不兼容")
	}
	// 必须在每条物理连接交给连接池之前设置，不能只配置池内恰好选中的连接。
	if _, err := full.ExecContext(ctx, "PRAGMA busy_timeout = 5000", nil); err != nil {
		return fmt.Errorf("配置 SQLite 忙等待失败：%w", err)
	}
	rows, err := full.QueryContext(ctx, "PRAGMA journal_mode = WAL", nil)
	if err != nil {
		return fmt.Errorf("配置 SQLite WAL 失败：%w", err)
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return fmt.Errorf("读取 SQLite 日志模式失败：%w", err)
	}
	if values[0] != "wal" && values[0] != "memory" {
		return fmt.Errorf("SQLite 日志模式无效：%v", values[0])
	}
	return ctx.Err()
}

var _ driver.Connector = (*sqlitePoolConnector)(nil)
