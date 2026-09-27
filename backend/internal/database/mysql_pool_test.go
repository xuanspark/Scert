package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

type poolTestConn struct {
	mysqlDriverConn
	reset     func(context.Context) error
	ping      func(context.Context) error
	execErr   error
	queryErr  error
	commitErr error
	closed    bool
	probes    int
	writes    int
	queries   int
	commits   int
	options   driver.TxOptions
}

func (c *poolTestConn) ResetSession(ctx context.Context) error {
	if c.reset != nil {
		return c.reset(ctx)
	}
	return nil
}

func (c *poolTestConn) Ping(ctx context.Context) error {
	c.probes++
	if c.ping != nil {
		return c.ping(ctx)
	}
	return ctx.Err()
}

func (c *poolTestConn) Close() error {
	c.closed = true
	return nil
}

func (c *poolTestConn) IsValid() bool { return !c.closed }

func (c *poolTestConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.writes++
	return driver.RowsAffected(1), c.execErr
}

func (c *poolTestConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.queries++
	return nil, c.queryErr
}

func (c *poolTestConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.options = opts
	return &poolTestTx{conn: c}, nil
}

type poolTestTx struct{ conn *poolTestConn }

func (tx *poolTestTx) Commit() error {
	tx.conn.commits++
	return tx.conn.commitErr
}

func (tx *poolTestTx) Rollback() error { return nil }

type poolTestConnector struct {
	driver.Connector
	connections []*poolTestConn
	calls       int
}

func (c *poolTestConnector) Connect(context.Context) (driver.Conn, error) {
	if c.calls >= len(c.connections) {
		return nil, errors.New("测试连接已耗尽")
	}
	conn := c.connections[c.calls]
	c.calls++
	return conn, nil
}

func TestMySQLPoolResetSession(t *testing.T) {
	t.Run("healthy_probe_has_deadline", func(t *testing.T) {
		conn := &poolTestConn{ping: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > mysqlReuseProbeTimeout {
				t.Fatal("复用探测必须具有有限超时")
			}
			return nil
		}}
		if err := (&mysqlPoolConn{conn}).ResetSession(t.Context()); err != nil {
			t.Fatal(err)
		}
		if conn.probes != 1 || conn.closed || conn.writes != 0 {
			t.Fatal("健康连接不应关闭或执行业务语句")
		}
	})
	t.Run("driver_rejects_before_probe", func(t *testing.T) {
		conn := &poolTestConn{reset: func(context.Context) error { return driver.ErrBadConn }}
		if err := (&mysqlPoolConn{conn}).ResetSession(t.Context()); !errors.Is(err, driver.ErrBadConn) || conn.probes != 0 {
			t.Fatal("原驱动拒绝连接后不能继续探测", err)
		}
	})
	t.Run("failed_probe_closes_connection", func(t *testing.T) {
		conn := &poolTestConn{ping: func(context.Context) error { return errors.New("连接中断") }}
		if err := (&mysqlPoolConn{conn}).ResetSession(t.Context()); !errors.Is(err, driver.ErrBadConn) || !conn.closed || conn.writes != 0 {
			t.Fatal("探测失败必须在写入前丢弃连接", err)
		}
	})
	t.Run("shorter_caller_deadline_wins", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		want, _ := ctx.Deadline()
		conn := &poolTestConn{ping: func(probe context.Context) error {
			got, ok := probe.Deadline()
			if !ok || !got.Equal(want) {
				t.Fatal("探测不能延长调用者截止时间")
			}
			<-probe.Done()
			return probe.Err()
		}}
		if err := (&mysqlPoolConn{conn}).ResetSession(ctx); !errors.Is(err, driver.ErrBadConn) || !conn.closed {
			t.Fatal("超时连接必须丢弃", err)
		}
	})
	t.Run("canceled_context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		conn := &poolTestConn{}
		if err := (&mysqlPoolConn{conn}).ResetSession(ctx); !errors.Is(err, driver.ErrBadConn) || !conn.closed || conn.writes != 0 {
			t.Fatal("取消后不能使用探测连接执行写入", err)
		}
	})
}

func TestMySQLPoolReplacesOnlyBeforeExecution(t *testing.T) {
	stale, fresh := &poolTestConn{}, &poolTestConn{}
	connector := &poolTestConnector{connections: []*poolTestConn{stale, fresh}}
	db := sql.OpenDB(&mysqlPoolConnector{connector})
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	stale.ping = func(context.Context) error { return errors.New("空闲连接失效") }
	if _, err := db.ExecContext(t.Context(), "UPDATE counter SET value = value + 1"); err != nil {
		t.Fatal(err)
	}
	if connector.calls != 2 || !stale.closed || stale.writes != 0 || fresh.writes != 1 {
		t.Fatal("只允许替换业务写入前失效的连接")
	}
}

func TestMySQLPoolDoesNotReplayUncertainOperations(t *testing.T) {
	uncertain := errors.New("响应丢失，执行结果不确定")
	for _, operation := range []string{"exec", "query", "commit"} {
		t.Run(operation, func(t *testing.T) {
			conn := &poolTestConn{execErr: uncertain, queryErr: uncertain, commitErr: uncertain}
			connector := &poolTestConnector{connections: []*poolTestConn{conn}}
			db := sql.OpenDB(&mysqlPoolConnector{connector})
			defer db.Close()
			if err := db.PingContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			var err error
			switch operation {
			case "exec":
				_, err = db.ExecContext(t.Context(), "UPDATE counter SET value = value + 1")
			case "query":
				_, err = db.QueryContext(t.Context(), "CALL change_and_return_counter()")
			case "commit":
				var tx *sql.Tx
				tx, err = db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				if conn.options.Isolation != driver.IsolationLevel(sql.LevelSerializable) || !conn.options.ReadOnly {
					t.Fatal("事务隔离级别和只读选项必须原样传递")
				}
				err = tx.Commit()
			}
			if !errors.Is(err, uncertain) || errors.Is(err, driver.ErrBadConn) || connector.calls != 1 {
				t.Fatal("结果不确定的操作必须保留原错误且不能重建连接重放", err)
			}
			if conn.writes+conn.queries+conn.commits != 1 || conn.probes != 2 {
				t.Fatal("操作只能执行一次，事务内不能插入额外探测")
			}
		})
	}
}
