package database

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

type sqliteDeadlineFaultConn struct {
	sqliteDriverConn
	commands []string
	failAt   int
	err      error
}

func (c *sqliteDeadlineFaultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.commands = append(c.commands, query)
	if len(c.commands) == c.failAt {
		return nil, c.err
	}
	return driver.RowsAffected(0), nil
}

func (c *sqliteDeadlineFaultConn) IsValid() bool { return true }

func TestSQLiteDeadlineFailureNeverReplaysOperation(t *testing.T) {
	for _, name := range []string{"setup_failure", "restore_failure", "operation_failure", "already_canceled"} {
		t.Run(name, func(t *testing.T) {
			failure := errors.New("注入的隔离测试错误")
			raw := &sqliteDeadlineFaultConn{err: failure}
			conn := &sqliteDeadlineConn{sqliteDriverConn: raw}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			wantRuns := 1
			switch name {
			case "setup_failure":
				raw.failAt = 1
				wantRuns = 0
			case "restore_failure":
				raw.failAt = 2
			case "already_canceled":
				cancel()
				wantRuns = 0
			}
			runs := 0
			_, err := sqliteWithDeadline(conn, ctx, func() (int, error) {
				runs++
				if name == "operation_failure" {
					return 0, failure
				}
				return 7, nil
			})
			if err == nil || runs != wantRuns || errors.Is(err, driver.ErrBadConn) {
				t.Fatalf("错误处理发生重放或丢失：执行次数=%d，错误=%v", runs, err)
			}
			if name == "setup_failure" || name == "restore_failure" {
				if conn.IsValid() || !errors.Is(conn.ResetSession(t.Context()), driver.ErrBadConn) {
					t.Fatal("配置状态不确定的连接仍可被复用")
				}
			}
			if name == "operation_failure" {
				if !errors.Is(err, failure) || len(raw.commands) != 2 || raw.commands[1] != "PRAGMA busy_timeout = 5000" || !conn.IsValid() {
					t.Fatalf("业务错误后没有保留错误或恢复连接配置：%v", raw.commands)
				}
			}
			if name == "already_canceled" && (len(raw.commands) != 0 || !errors.Is(err, context.Canceled)) {
				t.Fatal("已经取消的调用仍然修改了连接")
			}
			for _, command := range raw.commands {
				if !strings.HasPrefix(command, "PRAGMA busy_timeout = ") {
					t.Fatalf("执行了额外 SQL：%s", command)
				}
			}
		})
	}
}
