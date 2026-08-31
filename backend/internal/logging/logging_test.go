package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoggerWritesJSONAndUsesRestrictedPermissions(t *testing.T) {
	dir := t.TempDir()
	logger, err := New(dir, slog.LevelInfo)
	if err != nil {
		t.Fatalf("初始化日志失败：%v", err)
	}
	logger.Info("复审日志", "component", "test")
	if err := logger.Close(); err != nil {
		t.Fatalf("关闭日志失败：%v", err)
	}
	path := filepath.Join(dir, "scert.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志失败：%v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"msg":"复审日志"`) || !strings.Contains(text, `"component":"test"`) {
		t.Fatalf("日志内容不正确：%s", text)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("读取日志权限失败：%v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("日志文件权限不正确：%o", info.Mode().Perm())
		}
	}
}

func TestDailyFileWriterRotatesCapsAndRemovesExpiredLogs(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 31, 8, 0, 0, 0, time.Local)
	oldPath := filepath.Join(directory, "scert-20260701.log")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("写入过期日志失败：%v", err)
	}
	oldTime := now.AddDate(0, 0, -60)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("设置过期日志时间失败：%v", err)
	}

	writer, err := newDailyFileWriterWithOptions(directory, 32, 30, func() time.Time { return now })
	if err != nil {
		t.Fatalf("初始化日志轮转器失败：%v", err)
	}
	if _, err := writer.Write([]byte("first-day-entry\n")); err != nil {
		t.Fatalf("写入首日日志失败：%v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("过期日志未被清理：%v", err)
	}

	now = now.Add(24 * time.Hour)
	if _, err := writer.Write([]byte("second-day-entry\n")); err != nil {
		t.Fatalf("写入次日日志失败：%v", err)
	}
	rotatedPath := filepath.Join(directory, "scert-20260831.log")
	if data, err := os.ReadFile(rotatedPath); err != nil || !strings.Contains(string(data), "first-day-entry") {
		t.Fatalf("首日日志未正确轮转：data=%q err=%v", data, err)
	}

	activePath := filepath.Join(directory, "scert.log")
	before, err := os.Stat(activePath)
	if err != nil {
		t.Fatalf("读取活动日志状态失败：%v", err)
	}
	large := strings.Repeat("x", 64)
	if written, err := writer.Write([]byte(large)); err != nil || written != len(large) {
		t.Fatalf("超过容量的日志写入结果不正确：written=%d err=%v", written, err)
	}
	after, err := os.Stat(activePath)
	if err != nil {
		t.Fatalf("再次读取活动日志状态失败：%v", err)
	}
	if before.Size() != after.Size() || after.Size() > 32 {
		t.Fatalf("活动日志超过每日容量上限：before=%s after=%s", strconv.FormatInt(before.Size(), 10), strconv.FormatInt(after.Size(), 10))
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭日志轮转器失败：%v", err)
	}
}

func TestDailyFileWriterRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 普通用户环境通常无法创建符号链接")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target.log")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "scert.log")); err != nil {
		t.Fatal(err)
	}
	if _, err := newDailyFileWriter(directory); err == nil {
		t.Fatal("活动日志是符号链接时应拒绝启动")
	}
}

func TestDailyFileWriterKeepsDailyCapAfterRestart(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 31, 8, 0, 0, 0, time.Local)
	writer, err := newDailyFileWriterWithOptions(directory, 16, 30, func() time.Time { return now })
	if err != nil {
		t.Fatalf("初始化日志轮转器失败：%v", err)
	}
	if _, err := writer.Write([]byte("1234567890123456")); err != nil {
		t.Fatalf("写满每日日志容量失败：%v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭日志轮转器失败：%v", err)
	}

	restarted, err := newDailyFileWriterWithOptions(directory, 16, 30, func() time.Time { return now })
	if err != nil {
		t.Fatalf("重启日志轮转器失败：%v", err)
	}
	if _, err := restarted.Write([]byte("should-not-be-written")); err != nil {
		t.Fatalf("容量用尽后写入结果错误：%v", err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("再次关闭日志轮转器失败：%v", err)
	}

	info, err := os.Stat(filepath.Join(directory, "scert.log"))
	if err != nil {
		t.Fatalf("读取活动日志状态失败：%v", err)
	}
	if info.Size() != 16 {
		t.Fatalf("服务重启后每日日志容量被绕过：%d", info.Size())
	}
	if _, err := os.Stat(filepath.Join(directory, "scert-20260831.log")); !os.IsNotExist(err) {
		t.Fatalf("同日重启不应轮转活动日志：%v", err)
	}
}
