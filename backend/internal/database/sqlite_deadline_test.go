package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestSQLiteDeadlineAcrossExecutionPaths(t *testing.T) {
	for _, name := range []string{"exec", "query", "prepared_exec", "prepared_query", "transaction", "immediate_transaction"} {
		t.Run(name, func(t *testing.T) {
			first, second := newSQLiteConcurrencyPools(t)
			if name == "immediate_transaction" {
				var filename string
				var sequence int
				var databaseName string
				if err := first.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &databaseName, &filename); err != nil {
					t.Fatal(err)
				}
				cfg := config.Default(t.TempDir()).Database
				cfg.DSN = filename + "?_txlock=immediate"
				second = openSQLiteTestPool(t, cfg)
			}
			conn, err := second.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
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
			switch name {
			case "exec":
				_, err = conn.ExecContext(ctx, "UPDATE lock_probe SET value = 99 WHERE id = 1")
			case "query":
				var value int
				err = conn.QueryRowContext(ctx, "UPDATE lock_probe SET value = 99 WHERE id = 1 RETURNING value").Scan(&value)
			case "prepared_exec", "prepared_query":
				statement := "UPDATE lock_probe SET value = ? WHERE id = 1"
				if name == "prepared_query" {
					statement += " RETURNING value"
				}
				stmt, prepareErr := conn.PrepareContext(t.Context(), statement)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				defer stmt.Close()
				if name == "prepared_exec" {
					_, err = stmt.ExecContext(ctx, 99)
				} else {
					var value int
					err = stmt.QueryRowContext(ctx, 99).Scan(&value)
				}
			case "transaction":
				tx, beginErr := conn.BeginTx(t.Context(), nil)
				if beginErr != nil {
					t.Fatal(beginErr)
				}
				defer tx.Rollback()
				_, err = tx.ExecContext(ctx, "UPDATE lock_probe SET value = 99 WHERE id = 1")
				_ = tx.Rollback()
			case "immediate_transaction":
				var tx *sql.Tx
				tx, err = conn.BeginTx(ctx, nil)
				if tx != nil {
					_ = tx.Rollback()
				}
			}
			elapsed := time.Since(started)
			if !errors.Is(err, context.DeadlineExceeded) || elapsed > time.Second {
				t.Errorf("执行路径未遵守截止时间：耗时=%s，错误=%v", elapsed, err)
			}
			checkSQLiteConnection(t, t.Context(), conn)
			if err := writer.Rollback(); err != nil {
				t.Fatal(err)
			}
			if value := readSQLiteProbe(t, conn); value != 0 {
				t.Fatalf("超时写入产生了副作用：%d", value)
			}
			if _, err := conn.ExecContext(t.Context(), "UPDATE lock_probe SET value = 2 WHERE id = 1"); err != nil {
				t.Fatalf("超时后的同一连接无法继续写入：%v", err)
			}
		})
	}
}

func TestSQLitePoolWaitHonorsContext(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.MaxOpenConns = 1
	cfg.MaxIdleConns = 1
	db := openSQLiteTestPool(t, cfg)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := db.ExecContext(ctx, "SELECT 1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("连接池耗尽时没有响应截止时间：%v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatalf("等待取消后连接池未恢复：%v", err)
	}
}

func TestSQLiteDeadlinePreservesQueryMetadata(t *testing.T) {
	first, _ := newSQLiteConcurrencyPools(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	rows, err := first.QueryContext(ctx, "SELECT id, value FROM lock_probe")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 2 || columns[0].DatabaseTypeName() != "INTEGER" {
		t.Fatalf("包装后的结果元数据丢失：%v，错误=%v", columns, err)
	}
	if !rows.Next() {
		t.Fatalf("没有读取到查询结果：%v", rows.Err())
	}
	var id, value int
	if err := rows.Scan(&id, &value); err != nil || id != 1 || value != 0 {
		t.Fatalf("查询数据错误：id=%d，value=%d，错误=%v", id, value, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("查询结束状态错误：%v", rows.Err())
	}
}

func TestSQLiteOpenDeadlineWhileChangingJournalMode(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	if err := ensureSQLiteDir(cfg.DSN); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(t.Context(), "CREATE TABLE mode_probe (value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	writer, err := raw.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.ExecContext(t.Context(), "INSERT INTO mode_probe VALUES (7)"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	db, err := Open(ctx, cfg)
	if db != nil {
		_ = db.Close()
	}
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("初始化 WAL 遇到外部锁时没有及时失败：耗时=%s，错误=%v", time.Since(started), err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	reopened := openSQLiteTestPool(t, cfg)
	var value int
	if err := reopened.QueryRowContext(t.Context(), "SELECT value FROM mode_probe").Scan(&value); err != nil || value != 7 {
		t.Fatalf("初始化失败破坏了原有数据：%d，%v", value, err)
	}
}
