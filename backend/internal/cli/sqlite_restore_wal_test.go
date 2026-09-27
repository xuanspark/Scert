package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
)

func TestSQLiteRestoreAfterUncleanShutdown(t *testing.T) {
	if path := os.Getenv("SCERT_SQLITE_RESTORE_CRASH_DSN"); path != "" {
		cfg := config.Default("").Database
		cfg.DSN = path
		db, err := database.Open(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB.ExecContext(t.Context(), "UPDATE audit_logs SET action = 'wal-current' WHERE action = 'current'"); err != nil {
			t.Fatal(err)
		}
		// 故意不关闭数据库，模拟已提交 WAL 尚未检查点时进程退出。
		os.Exit(0)
	}
	for _, rollback := range []bool{false, true} {
		name := "restore"
		if rollback {
			name = "rollback_after_asset_failure"
		}
		t.Run(name, func(t *testing.T) {
			cfg, currentPath, input := prepareRestoreCommandFixture(t, rollback)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "-test.run=^TestSQLiteRestoreAfterUncleanShutdown$", "-test.timeout=20s")
			child.Env = append(os.Environ(), "SCERT_SQLITE_RESTORE_CRASH_DSN="+cfg.Database.DSN)
			if output, err := child.CombinedOutput(); err != nil {
				t.Fatalf("异常退出夹具创建失败：%v\n%s", err, output)
			}
			if info, err := os.Stat(cfg.Database.DSN + "-wal"); err != nil || info.Size() == 0 {
				t.Fatalf("夹具未留下真实 WAL：%v", err)
			}
			previous := configPath
			configPath = currentPath
			t.Cleanup(func() { configPath = previous })
			command := newRestoreCommand()
			var output strings.Builder
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs([]string{"--input", input})
			err = command.ExecuteContext(t.Context())
			snapshots, snapshotErr := filepath.Glob(cfg.Database.DSN + ".before-restore-*")
			if snapshotErr != nil || len(snapshots) != 1 {
				t.Fatalf("恢复前快照缺失：数量=%d，错误=%v", len(snapshots), snapshotErr)
			}
			if count, snapshotErr := auditActionCount(snapshots[0], "wal-current"); snapshotErr != nil || count != 1 {
				t.Fatalf("恢复前快照遗漏已提交的 WAL 数据：数量=%d，错误=%v", count, snapshotErr)
			}
			want := "restored"
			if rollback {
				want = "wal-current"
				if err == nil || !strings.Contains(err.Error(), "证书恢复目标必须是普通目录") {
					t.Fatalf("未触发预期的资产恢复失败：%v", err)
				}
			} else if err != nil {
				t.Fatalf("恢复失败：%v", err)
			}
			for _, action := range []string{"restored", "current", "wal-current"} {
				expected := 0
				if action == want {
					expected = 1
				}
				if count, err := auditActionCount(cfg.Database.DSN, action); err != nil || count != expected {
					t.Errorf("异常退出后的恢复内容不正确：动作=%s，数量=%d，期望=%d，错误=%v", action, count, expected, err)
				}
			}
		})
	}
}
