package database

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// 覆盖真实连接间的写入与检查点竞争，不将概率性压力测试当成上游缺陷的确定性复现。
func TestSQLiteWALCheckpointIntegrity(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "checkpoint.db")
	cfg.MaxOpenConns, cfg.MaxIdleConns = 3, 3
	db := openSQLiteTestPool(t, cfg)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var version string
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("检查点并发完整性测试引擎：%s", version)
	if _, err := db.ExecContext(ctx, `CREATE TABLE wal_integrity (
		writer INTEGER NOT NULL, sequence INTEGER NOT NULL, part INTEGER NOT NULL,
		value INTEGER NOT NULL, PRIMARY KEY (writer, sequence, part))`); err != nil {
		t.Fatal(err)
	}
	const batches = 256
	connections := make([]*sql.Conn, 3)
	for i := range connections {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		connections[i] = conn
	}
	start, writersDone := make(chan struct{}), make(chan struct{})
	results := make(chan error, 3)
	var writers sync.WaitGroup
	for writer := 0; writer < 2; writer++ {
		conn := connections[writer]
		writers.Add(1)
		go func() {
			defer writers.Done()
			defer conn.Close()
			<-start
			for sequence := 0; sequence < batches; sequence++ {
				if err := insertWALIntegrityPair(ctx, conn, writer, sequence); err != nil {
					results <- err
					return
				}
				runtime.Gosched()
			}
			results <- nil
		}()
	}
	checkpoint := connections[2]
	go func() {
		defer checkpoint.Close()
		<-start
		for {
			var busy, frames, copied int
			if err := checkpoint.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil {
				results <- err
				return
			}
			select {
			case <-writersDone:
				results <- nil
				return
			default:
				runtime.Gosched()
			}
		}
	}()
	close(start)
	writers.Wait()
	close(writersDone)
	for i := 0; i < 3; i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		return
	}
	var busy, frames, copied int
	if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &copied); err != nil || busy != 0 {
		t.Fatalf("最终检查点未完成：busy=%d，错误=%v", busy, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// 重新打开文件验证已持久化的内容，不能只检查仍在连接缓存中的结果。
	reopened := openSQLiteTestPool(t, cfg)
	var integrity string
	if err := reopened.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("检查点竞争后数据库结构损坏：%s，%v", integrity, err)
	}
	var count, sum, invalid int
	if err := reopened.QueryRowContext(ctx, `SELECT COUNT(*), SUM(value),
		SUM(CASE WHEN writer NOT IN (0, 1) OR sequence < 0 OR sequence >= ?
		OR part NOT IN (0, 1) OR value <> writer * 10000 + sequence * 2 + part THEN 1 ELSE 0 END)
		FROM wal_integrity`, batches).Scan(&count, &sum, &invalid); err != nil {
		t.Fatal(err)
	}
	wantSum := 2*batches*10000 + 2*batches*(2*batches-1)
	if count != 4*batches || sum != wantSum || invalid != 0 {
		t.Fatalf("检查点竞争后已提交数据不完整：条数=%d，总和=%d，错值=%d", count, sum, invalid)
	}
}

func insertWALIntegrityPair(ctx context.Context, conn *sql.Conn, writer, sequence int) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for part := 0; part < 2; part++ {
		if _, err := tx.ExecContext(ctx, "INSERT INTO wal_integrity VALUES (?, ?, ?, ?)",
			writer, sequence, part, writer*10000+sequence*2+part); err != nil {
			return fmt.Errorf("写入事务数据失败：%w", err)
		}
	}
	return tx.Commit()
}
