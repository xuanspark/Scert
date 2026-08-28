package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRestoreInputRequiresDirectoryAndConfig(t *testing.T) {
	if _, err := validateRestoreInput(t.TempDir()); err == nil {
		t.Fatal("缺少配置文件的备份目录未被拒绝")
	}
	file := filepath.Join(t.TempDir(), "backup.txt")
	if err := os.WriteFile(file, []byte("backup"), 0o600); err != nil {
		t.Fatalf("创建测试文件失败：%v", err)
	}
	if _, err := validateRestoreInput(file); err == nil {
		t.Fatal("普通文件路径未被拒绝")
	}
}

func TestValidateRestoreInputAcceptsCompleteDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scert.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("创建测试配置失败：%v", err)
	}
	result, err := validateRestoreInput(dir)
	if err != nil {
		t.Fatalf("有效备份目录未通过校验：%v", err)
	}
	if !strings.EqualFold(result, filepath.Clean(dir)) {
		t.Fatalf("备份目录未规范化：%s", result)
	}
}

func TestCopyFileExclusiveRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatalf("创建源文件失败：%v", err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("创建目标文件失败：%v", err)
	}
	if err := copyFileExclusive(source, target, 0o600); err == nil {
		t.Fatal("排他复制覆盖了已有文件")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("读取目标文件失败：%v", err)
	}
	if string(data) != "old" {
		t.Fatalf("目标文件内容被修改：%s", data)
	}
}

func TestSamePathNormalizesAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if !samePath(path, filepath.Join(dir, ".", "file")) {
		t.Fatal("相同路径未被识别")
	}
}
