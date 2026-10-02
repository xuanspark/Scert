package database

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestSQLiteProcessConcurrency(t *testing.T) {
	const writes = 50
	if path := os.Getenv("SCERT_SQLITE_TEST_PROCESS_FILE"); path != "" {
		cfg := config.Default(t.TempDir()).Database
		cfg.DSN = path
		db := openSQLiteTestPool(t, cfg)
		fmt.Fprintln(os.Stdout, "sqlite-worker-ready")
		for i := 0; i < writes; i++ {
			if _, err := db.ExecContext(t.Context(), "UPDATE lock_probe SET value = value + 1 WHERE id = 1"); err != nil {
				t.Fatalf("子进程第 %d 次写入失败：%v", i+1, err)
			}
		}
		return
	}
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "process.db")
	db := openSQLiteTestPool(t, cfg)
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE lock_probe (id INTEGER PRIMARY KEY, value INTEGER NOT NULL); INSERT INTO lock_probe VALUES (1, 0)"); err != nil {
		t.Fatal(err)
	}
	var version string
	if err := db.QueryRowContext(t.Context(), "SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("隔离多进程 SQLite 引擎版本：%s", version)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const processes = 3
	results := make(chan error, processes)
	ready := make(chan error, processes)
	writer, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.ExecContext(t.Context(), "UPDATE lock_probe SET value = 0 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	for i := 0; i < processes; i++ {
		wg.Go(func() {
			// 重用正式测试二进制，不生成脚本或启动项目外的数据库服务。
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSQLiteProcessConcurrency$", "-test.timeout=20s")
			cmd.Env = append(os.Environ(), "SCERT_SQLITE_TEST_PROCESS_FILE="+cfg.DSN)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				ready <- err
				return
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				ready <- err
				return
			}
			scanner := bufio.NewScanner(stdout)
			signaled := false
			for scanner.Scan() {
				if scanner.Text() == "sqlite-worker-ready" && !signaled {
					signaled = true
					ready <- nil
				}
			}
			waitErr := cmd.Wait()
			if !signaled {
				ready <- fmt.Errorf("子进程在就绪前退出：%v", waitErr)
			}
			if scanner.Err() != nil || waitErr != nil {
				results <- fmt.Errorf("隔离子进程失败：等待=%v，读取=%v\n%s", waitErr, scanner.Err(), stderr.String())
			}
		})
	}
	// 三个独立进程均已打开数据库后再释放写锁，保证实际出现重叠的锁竞争。
	for i := 0; i < processes; i++ {
		select {
		case err := <-ready:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Fatal("等待隔离子进程就绪超时")
		}
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	if value := readSQLiteProbe(t, db); value != processes*writes {
		t.Fatalf("多进程提交总数错误：得到 %d，期望 %d", value, processes*writes)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openSQLiteTestPool(t, cfg)
	if value := readSQLiteProbe(t, reopened); value != processes*writes {
		t.Fatalf("关闭连接后重新打开丢失数据：%d", value)
	}
}
