package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// 取消可能与驱动返回结果集同时发生；调用方拿不到结果集时也不能遗留读锁和文件句柄。
func TestSQLiteCanceledQueriesReleaseFiles(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			cfg := config.Default(t.TempDir()).Database
			cfg.DSN = filepath.Join(t.TempDir(), "close.db")
			cfg.MaxOpenConns, cfg.MaxIdleConns = 1, 1
			db, err := Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.DB.ExecContext(t.Context(), `CREATE TABLE close_probe (id INTEGER PRIMARY KEY);
				WITH RECURSIVE values_to_insert(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM values_to_insert WHERE n<128)
				INSERT INTO close_probe SELECT n FROM values_to_insert`); err != nil {
				t.Fatal(err)
			}
			query := func(ctx context.Context) (*sql.Rows, error) {
				return db.DB.QueryContext(ctx, "SELECT id FROM close_probe")
			}
			if prepared {
				stmt, err := db.DB.PrepareContext(t.Context(), "SELECT id FROM close_probe")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				query = func(ctx context.Context) (*sql.Rows, error) { return stmt.QueryContext(ctx) }
			}
			canceled := 0
			for attempt := 0; attempt < 2048; attempt++ {
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan struct{})
				go func() {
					// 交替让查询与取消先取得调度，不通过休眠或重试关闭来掩盖泄漏。
					for yield := 0; yield < attempt%8; yield++ {
						runtime.Gosched()
					}
					cancel()
					close(done)
				}()
				rows, err := query(ctx)
				if rows != nil {
					for rows.Next() {
						var value int
						if scanErr := rows.Scan(&value); scanErr != nil {
							err = scanErr
							break
						}
					}
					err = errors.Join(err, rows.Err(), rows.Close())
				}
				<-done
				cancel()
				if errors.Is(err, context.Canceled) {
					canceled++
				} else if err != nil {
					t.Fatal(err)
				}
			}
			if canceled == 0 {
				t.Fatal("没有实际覆盖取消中的查询")
			}
			// 跨平台检查已取消查询没有留下读事务；关闭后 Windows 还必须立即允许移动文件。
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			var busy, logFrames, checkpointed int
			if err := db.DB.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil || busy != 0 {
				t.Fatalf("查询结束后仍存在未释放的读事务：busy=%d，错误=%v", busy, err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(cfg.DSN, filepath.Join(filepath.Dir(cfg.DSN), "closed.db")); err != nil {
				t.Fatalf("关闭后数据库文件仍被占用：%v", err)
			}
			t.Logf("已验证 %d 次查询，其中 %d 次实际取消", 2048, canceled)
		})
	}
}
