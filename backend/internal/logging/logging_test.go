package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
