package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
)

func TestSQLiteRestoreRejectsActiveTransaction(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "reader"
		if write {
			name = "writer"
		}
		t.Run(name, func(t *testing.T) {
			cfg, path, input := prepareRestoreCommandFixture(t, false)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			db, err := database.Open(t.Context(), cfg.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx, err := db.DB.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if write {
				_, err = tx.ExecContext(t.Context(), "UPDATE audit_logs SET message = '占用中的事务'")
			} else {
				var count int
				err = tx.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_logs").Scan(&count)
			}
			if err != nil {
				t.Fatal(err)
			}
			previous := configPath
			configPath = path
			t.Cleanup(func() { configPath = previous })
			command := newRestoreCommand()
			var output strings.Builder
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs([]string{"--input", input})
			if err := command.ExecuteContext(t.Context()); err == nil {
				t.Fatal("存在外部事务时仍执行了恢复")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("预检失败修改了配置：%v", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if count, err := auditActionCount(cfg.Database.DSN, "current"); err != nil || count != 1 {
				t.Fatalf("预检失败修改了原有数据：数量=%d，错误=%v", count, err)
			}
		})
	}
}

func TestSQLiteRestoreTargetSidecarSafety(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run("orphan"+suffix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "absent.db")
			if err := os.WriteFile(path+suffix, []byte("保留的日志"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := prepareSQLiteRestoreTarget(t.Context(), path); err == nil {
				t.Fatal("孤立日志未被拒绝")
			}
			if data, err := os.ReadFile(path + suffix); err != nil || string(data) != "保留的日志" {
				t.Fatalf("孤立日志被修改：%v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("错误创建了数据库：%v", err)
			}
		})
		t.Run("directory"+suffix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "current.db")
			if err := createSQLiteTestDatabase(path, "current"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path+suffix, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := prepareSQLiteRestoreTarget(t.Context(), path); err == nil {
				t.Fatal("非普通日志路径未被拒绝")
			}
		})
	}
	t.Run("canceled", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent.db")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := prepareSQLiteRestoreTarget(ctx, path); err != context.Canceled {
			t.Fatalf("未保留取消错误：%v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("取消后创建了数据库：%v", err)
		}
	})
}
