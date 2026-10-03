package database

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// 模拟截止时间已到但取消通知尚未被调度的短暂窗口。
type sqliteDelayedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c sqliteDelayedDeadlineContext) Deadline() (time.Time, bool) {
	return c.deadline, true
}

func sqliteBusyFixture(t *testing.T) error {
	t.Helper()
	first, second := newSQLiteConcurrencyPools(t)
	writer, err := first.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.ExecContext(t.Context(), "UPDATE lock_probe SET value = 1 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	conn, err := second.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(value any) error {
		raw := value.(*sqliteDeadlineConn).sqliteDriverConn
		if _, err := raw.ExecContext(t.Context(), "PRAGMA busy_timeout = 1", nil); err != nil {
			return err
		}
		defer raw.ExecContext(context.Background(), "PRAGMA busy_timeout = 5000", nil)
		_, err := raw.ExecContext(t.Context(), "BEGIN IMMEDIATE", nil)
		return err
	})
	var busy *sqlite.Error
	if !errors.As(err, &busy) || busy.Code()&255 != 5 {
		t.Fatalf("未获得真实 SQLite 锁冲突：%v", err)
	}
	return err
}

func TestSQLiteDeadlineDelayedCancellation(t *testing.T) {
	busy := sqliteBusyFixture(t)
	for _, name := range []string{"expired_busy", "early_busy", "expired_success", "expired_other_error", "canceled_busy", "expired_restore_failure"} {
		t.Run(name, func(t *testing.T) {
			raw := &sqliteDeadlineFaultConn{err: errors.New("注入恢复失败")}
			conn := &sqliteDeadlineConn{sqliteDriverConn: raw}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			deadline := time.Now().Add(10 * time.Millisecond)
			if name == "early_busy" || name == "canceled_busy" {
				deadline = time.Now().Add(time.Second)
			}
			ctx := sqliteDelayedDeadlineContext{Context: parent, deadline: deadline}
			operationErr := busy
			if name == "expired_success" {
				operationErr = nil
			} else if name == "expired_other_error" {
				operationErr = errors.New("业务执行失败")
			} else if name == "expired_restore_failure" {
				raw.failAt = 2
			}
			runs := 0
			result, err := sqliteWithDeadline(conn, ctx, func() (int, error) {
				runs++
				if name == "canceled_busy" {
					cancel()
				} else if name != "early_busy" {
					time.Sleep(time.Until(deadline) + time.Millisecond)
				}
				return 7, operationErr
			})
			if runs != 1 || result != 7 || errors.Is(err, driver.ErrBadConn) {
				t.Fatalf("执行次数、结果或错误改变：次数=%d，结果=%d，错误=%v", runs, result, err)
			}
			wantDeadline := name == "expired_busy" || name == "expired_restore_failure"
			if errors.Is(err, context.DeadlineExceeded) != wantDeadline {
				t.Fatalf("截止时间错误判定不符：%v", err)
			}
			if errors.Is(err, context.Canceled) != (name == "canceled_busy") || !errors.Is(err, operationErr) {
				t.Fatalf("未保留取消或原始执行结果：%v", err)
			}
			if name == "expired_restore_failure" {
				if conn.IsValid() || !errors.Is(err, raw.err) {
					t.Fatalf("恢复失败后仍复用连接或丢失错误：%v", err)
				}
			} else if !conn.IsValid() || len(raw.commands) != 2 || raw.commands[1] != "PRAGMA busy_timeout = 5000" {
				t.Fatalf("连接等待配置未恢复：%v", raw.commands)
			}
		})
	}
}
