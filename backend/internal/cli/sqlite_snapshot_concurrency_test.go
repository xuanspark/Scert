package cli

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
)

func TestSQLiteSnapshotWhileWriterIsActive(t *testing.T) {
	cfg := config.Default(t.TempDir())
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.ExecContext(t.Context(), "CREATE TABLE snapshot_probe (id INTEGER PRIMARY KEY, value INTEGER); INSERT INTO snapshot_probe VALUES (1, 7)"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(cfg.Database.DSN + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("没有实际生成 WAL 数据：%v", err)
	}
	writer, err := db.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.ExecContext(t.Context(), "UPDATE snapshot_probe SET value = 99 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"uncommitted", "committed"} {
		t.Run(name, func(t *testing.T) {
			want := 7
			if name == "committed" {
				if err := writer.Commit(); err != nil {
					t.Fatal(err)
				}
				want = 99
			}
			output := filepath.Join(t.TempDir(), "snapshot.db")
			if err := backupSQLiteDatabase(t.Context(), cfg, output); err != nil {
				t.Fatalf("活动文件库快照失败：%v", err)
			}
			snapshot, err := sql.Open("sqlite", output)
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			var value int
			if err := snapshot.QueryRowContext(t.Context(), "SELECT value FROM snapshot_probe WHERE id = 1").Scan(&value); err != nil || value != want {
				t.Fatalf("快照没有保存对应提交状态：得到 %d，期望 %d，错误=%v", value, want, err)
			}
		})
	}
}
