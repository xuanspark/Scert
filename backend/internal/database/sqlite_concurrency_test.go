package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func newSQLiteConcurrencyPools(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
	cfg := config.Default(t.TempDir()).Database
	first := openSQLiteTestPool(t, cfg)
	second := openSQLiteTestPool(t, cfg)
	if _, err := first.ExecContext(t.Context(), "CREATE TABLE lock_probe (id INTEGER PRIMARY KEY, value INTEGER NOT NULL); INSERT INTO lock_probe VALUES (1, 0)"); err != nil {
		t.Fatalf("创建隔离并发数据失败：%v", err)
	}
	return first, second
}

func readSQLiteProbe(t *testing.T, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) int {
	t.Helper()
	var value int
	if err := db.QueryRowContext(t.Context(), "SELECT value FROM lock_probe WHERE id = 1").Scan(&value); err != nil {
		t.Fatalf("读取隔离并发数据失败：%v", err)
	}
	return value
}

func TestSQLiteConcurrentReadersAndWriter(t *testing.T) {
	first, second := newSQLiteConcurrencyPools(t)
	reader, err := first.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	if value := readSQLiteProbe(t, reader); value != 0 {
		t.Fatalf("初始快照错误：%d", value)
	}
	writer, err := second.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.ExecContext(t.Context(), "UPDATE lock_probe SET value = 1 WHERE id = 1"); err != nil {
		t.Fatalf("读事务存在时写入失败：%v", err)
	}
	if value := readSQLiteProbe(t, first); value != 0 {
		t.Fatalf("其他连接读到了未提交数据：%d", value)
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("读事务存在时提交失败：%v", err)
	}
	if value := readSQLiteProbe(t, reader); value != 0 {
		t.Fatalf("已有读事务的快照发生漂移：%d", value)
	}
	if value := readSQLiteProbe(t, first); value != 1 {
		t.Fatalf("新查询没有读到提交数据：%d", value)
	}
}

func TestSQLiteWriterWaitsForOtherPool(t *testing.T) {
	first, second := newSQLiteConcurrencyPools(t)
	writer, err := first.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.ExecContext(t.Context(), "UPDATE lock_probe SET value = 1 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := second.ExecContext(ctx, "UPDATE lock_probe SET value = value + 1 WHERE id = 1")
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("持锁期间竞争写入没有等待：%v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("释放写锁后等待方未成功写入：%v", err)
		}
	case <-ctx.Done():
		t.Fatalf("释放写锁后等待方没有结束：%v", ctx.Err())
	}
	if value := readSQLiteProbe(t, second); value != 2 {
		t.Fatalf("两次提交结果不正确：%d", value)
	}
}

func TestSQLiteConcurrentWritesAcrossPools(t *testing.T) {
	first, second := newSQLiteConcurrencyPools(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	const workers, writes = 8, 25
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		db := []*sql.DB{first, second}[i%2]
		wg.Go(func() {
			<-start
			for j := 0; j < writes; j++ {
				if _, err := db.ExecContext(ctx, "UPDATE lock_probe SET value = value + 1 WHERE id = 1"); err != nil {
					results <- fmt.Errorf("第 %d 次并发写入失败：%w", j+1, err)
					return
				}
			}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	if value := readSQLiteProbe(t, first); value != workers*writes {
		t.Fatalf("并发写入丢失：得到 %d，期望 %d", value, workers*writes)
	}
	if first.Stats().InUse != 0 || second.Stats().InUse != 0 {
		t.Fatal("并发操作结束后连接未释放")
	}
}

func TestSQLiteTransactionRollbackAndCancellation(t *testing.T) {
	for _, name := range []string{"rollback", "cancel"} {
		t.Run(name, func(t *testing.T) {
			first, second := newSQLiteConcurrencyPools(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tx, err := first.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, "UPDATE lock_probe SET value = 9 WHERE id = 1"); err != nil {
				t.Fatal(err)
			}
			if name == "cancel" {
				cancel()
				if err := tx.Commit(); !errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone) {
					t.Fatalf("取消后的事务没有拒绝提交：%v", err)
				}
			} else if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			// 独立连接池能够写入，证明回滚实际释放了锁，而非只返回错误。
			if _, err := second.ExecContext(t.Context(), "UPDATE lock_probe SET value = value + 1 WHERE id = 1"); err != nil {
				t.Fatalf("事务结束后写锁未释放：%v", err)
			}
			if value := readSQLiteProbe(t, second); value != 1 {
				t.Fatalf("被取消或回滚的数据仍然存在：%d", value)
			}
		})
	}
}

func TestSQLiteLockWaitCancellation(t *testing.T) {
	first, second := newSQLiteConcurrencyPools(t)
	writer, err := first.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.ExecContext(t.Context(), "UPDATE lock_probe SET value = 1 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = second.ExecContext(ctx, "UPDATE lock_probe SET value = 99 WHERE id = 1")
	elapsed := time.Since(started)
	t.Logf("写锁等待取消耗时：%s", elapsed)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("等待中的写入没有返回超时错误：%v", err)
	}
	if elapsed > time.Second {
		t.Errorf("取消响应超过 1 秒：%s", elapsed)
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if value := readSQLiteProbe(t, second); value != 0 {
		t.Fatalf("被取消的写入产生了持久化副作用：%d", value)
	}
	if _, err := second.ExecContext(t.Context(), "UPDATE lock_probe SET value = 2 WHERE id = 1"); err != nil {
		t.Fatalf("取消后连接不能继续使用：%v", err)
	}
}
