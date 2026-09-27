package database

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"
)

func TestMySQLRealPoolRecovery(t *testing.T) {
	f := startRealMySQL(t)
	ctx := t.Context()
	t.Run("idle_pool_survives_restart", func(t *testing.T) {
		db := mysqlRecoveryDatabase(t, f)
		if db.DB.Stats().Idle != 1 {
			t.Fatal("重启前必须有真实空闲连接")
		}
		f.stop(t)
		f.start(t, false)
		// 不关闭应用池、不调用额外 Ping；第一条业务写入必须成功且只生效一次。
		if _, err := db.DB.ExecContext(ctx, "UPDATE pool_recovery SET value = value + 1 WHERE id = 1"); err != nil {
			t.Fatal("数据库重启后第一条写入失败", err)
		}
		mysqlAssertRecoveryValue(t, db.DB, 1)
	})
	t.Run("prepared_statement_survives_idle_disconnect", func(t *testing.T) {
		db := mysqlRecoveryDatabase(t, f)
		stmt, err := db.DB.PrepareContext(ctx, "UPDATE pool_recovery SET value = value + ? WHERE id = ?")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		mysqlKillPoolConnection(t, f, db.DB)
		if _, err := stmt.ExecContext(ctx, 2, 1); err != nil {
			t.Fatal("空闲断线后预编译语句失败", err)
		}
		mysqlAssertRecoveryValue(t, db.DB, 2)
	})
	t.Run("query_and_migration_survive_idle_disconnect", func(t *testing.T) {
		db := mysqlRecoveryDatabase(t, f)
		mysqlKillPoolConnection(t, f, db.DB)
		mysqlAssertRecoveryValue(t, db.DB, 0)
		mysqlKillPoolConnection(t, f, db.DB)
		if err := Migrate(ctx, db.DB, "mysql"); err != nil {
			t.Fatal("空闲断线后首次迁移失败", err)
		}
		mysqlAssertRecoveryValue(t, db.DB, 0)
	})
	t.Run("parameter_conversion_and_commit", func(t *testing.T) {
		db := mysqlRecoveryDatabase(t, f)
		var value string
		if err := db.DB.QueryRowContext(ctx, "SELECT ?", uint64(math.MaxUint64)).Scan(&value); err != nil || value != "18446744073709551615" {
			t.Fatal("驱动无符号整数参数转换必须保留", err)
		}
		tx, err := db.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		stmt, err := tx.PrepareContext(ctx, "UPDATE pool_recovery SET value = ? WHERE id = ?")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if _, err := stmt.ExecContext(ctx, 7, 1); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		mysqlAssertRecoveryValue(t, db.DB, 7)
	})
	t.Run("transaction_options_and_rollback", func(t *testing.T) {
		db := mysqlRecoveryDatabase(t, f)
		writable, err := db.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer writable.Rollback()
		if _, err := writable.ExecContext(ctx, "UPDATE pool_recovery SET value = 40 WHERE id = 1"); err != nil {
			t.Fatal(err)
		}
		if err := writable.Rollback(); err != nil {
			t.Fatal(err)
		}
		mysqlKillPoolConnection(t, f, db.DB)
		tx, err := db.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, "UPDATE pool_recovery SET value = 99 WHERE id = 1"); err == nil {
			t.Fatal("只读事务不应接受写入")
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		mysqlAssertRecoveryValue(t, db.DB, 0)
	})
	t.Run("active_transaction_is_not_replayed", func(t *testing.T) {
		db := mysqlRecoveryDatabase(t, f)
		tx, err := db.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, "UPDATE pool_recovery SET value = 99 WHERE id = 1"); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := tx.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := f.admin.ExecContext(ctx, "KILL CONNECTION ?", id); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err == nil {
			t.Fatal("连接中断后的事务不能报告提交成功")
		}
		mysqlAssertRecoveryValue(t, db.DB, 0)
	})
	t.Run("database_down_is_bounded_and_then_recovers", func(t *testing.T) {
		db := mysqlRecoveryDatabase(t, f)
		f.stop(t)
		bounded, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, err := db.DB.ExecContext(bounded, "UPDATE pool_recovery SET value = 99 WHERE id = 1")
		if err == nil || time.Since(started) > 3*time.Second {
			t.Fatal("数据库不可用时必须有界失败", err)
		}
		f.start(t, false)
		mysqlAssertRecoveryValue(t, db.DB, 0)
	})
}

func mysqlRecoveryDatabase(t *testing.T, f *realMySQL) *Database {
	t.Helper()
	db := f.database(t)
	db.DB.SetMaxOpenConns(1)
	if _, err := db.DB.ExecContext(t.Context(), "CREATE TABLE pool_recovery (id BIGINT PRIMARY KEY, value BIGINT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(t.Context(), "INSERT INTO pool_recovery VALUES (1, 0)"); err != nil {
		t.Fatal(err)
	}
	return db
}

func mysqlKillPoolConnection(t *testing.T, f *realMySQL, db *sql.DB) {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(t.Context(), "SELECT CONNECTION_ID()").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.ExecContext(t.Context(), "KILL CONNECTION ?", id); err != nil {
		t.Fatal(err)
	}
}

func mysqlAssertRecoveryValue(t *testing.T, db *sql.DB, want int64) {
	t.Helper()
	var got int64
	if err := db.QueryRowContext(t.Context(), "SELECT value FROM pool_recovery WHERE id = 1").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("写入次数或回滚结果不符合预期：得到 %d，期望 %d", got, want)
	}
}
