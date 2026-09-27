package cli

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealBackupSnapshot(t *testing.T) {
	server := mysqltest.New(t)
	for _, scenario := range []struct {
		name string
		idle int
	}{
		{name: "without_idle_connections", idle: 0},
		{name: "with_idle_connections", idle: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cfg := server.Database(t)
			cfg.MaxIdleConns = scenario.idle
			db, err := database.Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, table := range []string{"a_snapshot", "b_snapshot"} {
				if _, err := db.DB.ExecContext(t.Context(), "CREATE TABLE "+table+" (id INT PRIMARY KEY, epoch INT NOT NULL) ENGINE=InnoDB"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.DB.ExecContext(t.Context(), "INSERT INTO "+table+" VALUES (1, 0)"); err != nil {
					t.Fatal(err)
				}
			}
			parsed, err := mysql.ParseDSN(cfg.DSN)
			if err != nil {
				t.Fatal("解析隔离数据库连接失败")
			}
			locker, err := server.Admin().Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer locker.Close()
			qualified := quoteMySQLIdentifier(parsed.DBName) + ".`b_snapshot`"
			if _, err := locker.ExecContext(t.Context(), "LOCK TABLES "+qualified+" WRITE"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = locker.ExecContext(ctx, "UNLOCK TABLES")
			}()
			output := filepath.Join(server.Root(), scenario.name+".sql")
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- backupMySQLDatabase(ctx, cfg, output) }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("备份协程未按期结束")
				}
			})
			waitForMySQLBackupLock(t, ctx, server.Admin(), parsed.DBName)
			content, err := os.ReadFile(output + ".tmp")
			if err != nil || !strings.Contains(string(content), "INSERT INTO `a_snapshot`") {
				t.Fatal("未到达首表已导出、第二张表等待锁的测试边界")
			}
			// 真实状态依次为 (0,0)、(1,0)、(1,1)，从未出现 (0,1)。
			// 更新第一张表后，再在持锁连接更新第二张表，避免依赖调度概率。
			if _, err := db.DB.ExecContext(ctx, "UPDATE a_snapshot SET epoch = 1"); err != nil {
				t.Fatal(err)
			}
			if _, err := locker.ExecContext(ctx, "UPDATE "+qualified+" SET epoch = 1"); err != nil {
				t.Fatal(err)
			}
			if _, err := locker.ExecContext(ctx, "UNLOCK TABLES"); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				done <- err
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("真实备份未按期完成")
			}
			restored := server.Database(t)
			if err := restoreMySQL(t.Context(), restored, output); err != nil {
				t.Fatal(err)
			}
			verify, err := database.Open(t.Context(), restored)
			if err != nil {
				t.Fatal(err)
			}
			defer verify.Close()
			var first, second int
			if err := verify.DB.QueryRowContext(t.Context(), "SELECT a.epoch, b.epoch FROM a_snapshot a JOIN b_snapshot b ON a.id = b.id").Scan(&first, &second); err != nil {
				t.Fatal(err)
			}
			if first != 0 || second != 0 {
				t.Fatalf("跨表备份不是同一读取快照：首表=%d，次表=%d", first, second)
			}
		})
	}
}

func waitForMySQLBackupLock(t *testing.T, ctx context.Context, admin *sql.DB, name string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE DB = ? AND STATE LIKE '%lock%' AND (INFO = 'SHOW CREATE TABLE `b_snapshot`' OR INFO = 'SELECT * FROM `b_snapshot`')", name).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("未观察到备份在第二张表等待真实数据库锁")
		case <-ticker.C:
		}
	}
}
