package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"runtime"
	"time"

	"github.com/go-sql-driver/mysql"
)

const mysqlReuseProbeTimeout = 2 * time.Second

func openMySQLPool(dsn string) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 MySQL 连接池配置失败")
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 MySQL 连接器失败")
	}
	// 驱动在 Windows 上没有空闲套接字检查；Linux 继续使用驱动原有检查，避免额外往返。
	if runtime.GOOS == "windows" && cfg.CheckConnLiveness {
		connector = &mysqlPoolConnector{Connector: connector}
	}
	return sql.OpenDB(connector), nil
}

// 保留锁定驱动实现的全部连接接口，不能因包装丢失事务选项、参数转换或上下文支持。
type mysqlDriverConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.Execer
	driver.ExecerContext
	driver.Queryer
	driver.QueryerContext
	driver.NamedValueChecker
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

type mysqlPoolConnector struct {
	driver.Connector
}

func (c *mysqlPoolConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	full, ok := conn.(mysqlDriverConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("MySQL 驱动连接接口不兼容")
	}
	return &mysqlPoolConn{mysqlDriverConn: full}, nil
}

type mysqlPoolConn struct {
	mysqlDriverConn
}

func (c *mysqlPoolConn) ResetSession(ctx context.Context) error {
	if err := c.mysqlDriverConn.ResetSession(ctx); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, mysqlReuseProbeTimeout)
	defer cancel()
	if err := c.mysqlDriverConn.Ping(probeCtx); err != nil {
		_ = c.mysqlDriverConn.Close()
		// 仅在业务语句发送前淘汰连接；不得把执行或提交错误改成可重试错误。
		return driver.ErrBadConn
	}
	return nil
}

var _ driver.Connector = (*mysqlPoolConnector)(nil)
var _ mysqlDriverConn = (*mysqlPoolConn)(nil)
