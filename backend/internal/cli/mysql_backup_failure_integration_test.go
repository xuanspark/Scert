package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealBackupFailureBoundaries(t *testing.T) {
	server := mysqltest.New(t)
	var originalGlobalTimeout int
	if err := server.Admin().QueryRowContext(t.Context(), "SELECT @@GLOBAL.lock_wait_timeout").Scan(&originalGlobalTimeout); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var currentGlobalTimeout int
		if err := server.Admin().QueryRowContext(ctx, "SELECT @@GLOBAL.lock_wait_timeout").Scan(&currentGlobalTimeout); err != nil || currentGlobalTimeout != originalGlobalTimeout {
			t.Errorf("备份不能修改数据库全局锁等待配置：%v", err)
		}
	})
	for _, scenario := range []struct {
		name string
		ddl  string
	}{
		{name: "reject_nontransactional_table", ddl: "CREATE TABLE %s (id INT PRIMARY KEY) ENGINE=MyISAM"},
		{name: "reject_view", ddl: "CREATE VIEW %s AS SELECT 1 AS id"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cfg := server.Database(t)
			parsed, err := mysql.ParseDSN(cfg.DSN)
			if err != nil {
				t.Fatal("解析隔离数据库连接失败")
			}
			qualified := quoteMySQLIdentifier(parsed.DBName) + ".`unsupported_object`"
			if _, err := server.Admin().ExecContext(t.Context(), fmt.Sprintf(scenario.ddl, qualified)); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(server.Root(), scenario.name+".sql")
			original := []byte("原有备份不能因失败被替换\n")
			writeMySQLBackupFixture(t, output, original)
			err = backupMySQLDatabase(t.Context(), cfg, output)
			if err == nil || !strings.Contains(err.Error(), "仅支持 InnoDB 基础表") {
				t.Fatalf("非事务对象不能被当作一致性备份成功导出：%v", err)
			}
			assertMySQLBackupFile(t, output, original)
			assertMySQLBackupAbsent(t, output+".tmp")
		})
	}

	for _, scenario := range []struct {
		name    string
		cancel  bool
		timeout bool
	}{
		{name: "cancel_after_first_table", cancel: true},
		{name: "query_failure_after_first_table"},
		{name: "metadata_lock_timeout", timeout: true},
	} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing_output_%t", scenario.name, existing), func(t *testing.T) {
				cfg := server.Database(t)
				db, err := database.Open(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				for _, table := range []string{"a_snapshot", "b_snapshot"} {
					if _, err := db.DB.ExecContext(t.Context(), "CREATE TABLE "+table+" (id INT PRIMARY KEY) ENGINE=InnoDB"); err != nil {
						t.Fatal(err)
					}
					if _, err := db.DB.ExecContext(t.Context(), "INSERT INTO "+table+" VALUES (1)"); err != nil {
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
					if _, err := locker.ExecContext(ctx, "UNLOCK TABLES"); err != nil {
						t.Error(err)
					}
				}()
				output := filepath.Join(server.Root(), fmt.Sprintf("%s-%t.sql", scenario.name, existing))
				original := []byte("已完成的历史备份\n")
				if existing {
					writeMySQLBackupFixture(t, output, original)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				done := make(chan struct{})
				var backupErr error
				go func() {
					defer close(done)
					backupErr = backupMySQLDatabase(ctx, cfg, output)
				}()
				t.Cleanup(func() {
					cancel()
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("备份协程未按期结束")
					}
				})
				connectionID := waitForMySQLBackupLock(t, ctx, server.Admin(), parsed.DBName)
				content, err := os.ReadFile(output + ".tmp")
				if err != nil || !bytes.Contains(content, []byte("INSERT INTO `a_snapshot`")) {
					t.Fatal("失败必须注入在首表已经写入备份、第二张表等待锁之后")
				}
				if scenario.cancel {
					cancel()
				} else if !scenario.timeout {
					// 只终止本次已确认等待锁的备份语句，不终止连接或共享数据库。
					if _, err := server.Admin().ExecContext(ctx, fmt.Sprintf("KILL QUERY %d", connectionID)); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("备份失败后未及时退出")
				}
				if scenario.cancel {
					if !errors.Is(backupErr, context.Canceled) {
						t.Fatalf("备份未保留取消错误：%v", backupErr)
					}
				} else {
					var sqlErr *mysql.MySQLError
					expectedCode := uint16(1317)
					if scenario.timeout {
						expectedCode = 1205
					}
					if !errors.As(backupErr, &sqlErr) || sqlErr.Number != expectedCode {
						t.Fatalf("备份未保留真实语句中断错误：%v", backupErr)
					}
				}
				if existing {
					assertMySQLBackupFile(t, output, original)
				} else {
					assertMySQLBackupAbsent(t, output)
				}
				assertMySQLBackupAbsent(t, output+".tmp")
				// 备份事务曾读取首表；失败后必须释放元数据锁，不能阻塞后续维护。
				verifyCtx, verifyCancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer verifyCancel()
				if _, err := db.DB.ExecContext(verifyCtx, "ALTER TABLE a_snapshot ADD COLUMN released INT NULL"); err != nil {
					t.Fatalf("备份失败后未释放首表元数据锁：%v", err)
				}
			})
		}
	}

	t.Run("temporary_file_conflict", func(t *testing.T) {
		cfg := server.Database(t)
		output := filepath.Join(server.Root(), "conflict.sql")
		original := []byte("保留原有完整备份\n")
		temporary := []byte("其他备份操作持有的临时文件\n")
		writeMySQLBackupFixture(t, output, original)
		writeMySQLBackupFixture(t, output+".tmp", temporary)
		if err := backupMySQLDatabase(t.Context(), cfg, output); !errors.Is(err, os.ErrExist) {
			t.Fatalf("临时文件冲突必须拒绝：%v", err)
		}
		assertMySQLBackupFile(t, output, original)
		assertMySQLBackupFile(t, output+".tmp", temporary)
	})

	t.Run("publish_failure", func(t *testing.T) {
		cfg := server.Database(t)
		output := filepath.Join(server.Root(), "occupied-output")
		if err := os.Mkdir(output, 0o700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(output, "marker.txt")
		original := []byte("已有目录与内容必须保留\n")
		writeMySQLBackupFixture(t, marker, original)
		err := backupMySQLDatabase(t.Context(), cfg, output)
		var linkErr *os.LinkError
		if !errors.As(err, &linkErr) || linkErr.Op != "rename" {
			t.Fatalf("必须在文件发布阶段失败：%v", err)
		}
		assertMySQLBackupFile(t, marker, original)
		assertMySQLBackupAbsent(t, output+".tmp")
	})
}

func writeMySQLBackupFixture(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertMySQLBackupFile(t *testing.T, path string, expected []byte) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(content, expected) {
		t.Fatalf("已有备份或其他操作的文件被修改：%s，%v", path, err)
	}
}

func assertMySQLBackupAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("不应遗留不完整备份或本次临时文件：%s，%v", path, err)
	}
}
