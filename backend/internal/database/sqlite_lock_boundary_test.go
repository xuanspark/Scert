package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestSQLiteNativeBusyWaitBoundaries(t *testing.T) {
	for _, name := range []string{"busy_timeout", "manual_cancel_without_deadline"} {
		t.Run(name, func(t *testing.T) {
			first, second := newSQLiteConcurrencyPools(t)
			writer, err := first.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback()
			if _, err := writer.ExecContext(t.Context(), "UPDATE lock_probe SET value = 1 WHERE id = 1"); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if name == "manual_cancel_without_deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				timer := time.AfterFunc(150*time.Millisecond, cancel)
				defer timer.Stop()
			}
			started := time.Now()
			_, err = second.ExecContext(ctx, "UPDATE lock_probe SET value = 99 WHERE id = 1")
			elapsed := time.Since(started)
			t.Logf("原生忙等待边界：%s，耗时=%s，错误=%v", name, elapsed, err)
			if name == "busy_timeout" {
				var sqliteErr *sqlite.Error
				if !errors.As(err, &sqliteErr) || sqliteErr.Code() != 5 || elapsed < 4*time.Second {
					t.Errorf("无截止时间的等待没有遵守默认忙等待：耗时=%s，错误=%v", elapsed, err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Errorf("手动取消后错误不正确：%v", err)
			}
			// 本测试记录现存原生等待上限，不代表无截止时间的取消可在一秒内完成。
			if elapsed > 7*time.Second {
				t.Errorf("原生忙等待超过允许的测试上限：%s", elapsed)
			}
			if err := writer.Rollback(); err != nil {
				t.Fatal(err)
			}
			if value := readSQLiteProbe(t, second); value != 0 {
				t.Fatalf("失败写入产生了副作用：%d", value)
			}
		})
	}
}
