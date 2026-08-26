package secretstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreEncryptsAndRestoresValues(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	reference := "secret://dns/cloudflare/test"
	secret := "plain-api-token"
	if err := store.Put(t.Context(), reference, map[string]string{"api_token": secret}); err != nil {
		t.Fatalf("保存敏感信息失败：%v", err)
	}
	content, err := os.ReadFile(filepath.Join(directory, "secrets.json"))
	if err != nil {
		t.Fatalf("读取敏感信息文件失败：%v", err)
	}
	if strings.Contains(string(content), secret) {
		t.Fatal("敏感信息文件中不应出现明文凭据")
	}
	value, err := store.Get(t.Context(), reference)
	if err != nil {
		t.Fatalf("读取敏感信息失败：%v", err)
	}
	if value["api_token"] != secret {
		t.Fatalf("敏感信息解密结果不正确：%v", value)
	}
}

func TestStoreSupportsMultipleUpdates(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	if err := store.Put(t.Context(), "secret://dns/cloudflare/one", map[string]string{"api_token": "first"}); err != nil {
		t.Fatalf("第一次保存失败：%v", err)
	}
	if err := store.Put(t.Context(), "secret://dns/cloudflare/two", map[string]string{"api_token": "second"}); err != nil {
		t.Fatalf("第二次保存失败：%v", err)
	}
	value, err := store.Get(t.Context(), "secret://dns/cloudflare/one")
	if err != nil || value["api_token"] != "first" {
		t.Fatalf("多次保存后读取失败：value=%v err=%v", value, err)
	}
}

func TestWriteAtomicFileReplacesExistingFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "secrets.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("写入旧敏感信息文件失败：%v", err)
	}
	if err := writeAtomicFile(path, []byte("new"), 0o600, os.Rename); err != nil {
		t.Fatalf("原子替换敏感信息文件失败：%v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取替换后的敏感信息文件失败：%v", err)
	}
	if string(content) != "new" {
		t.Fatalf("敏感信息文件内容未更新：%q", content)
	}
	assertNoSecretTempFiles(t, directory)
}

func TestWriteAtomicFileKeepsOldFileWhenRenameFails(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "secrets.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("写入旧敏感信息文件失败：%v", err)
	}
	err := writeAtomicFile(path, []byte("new"), 0o600, func(string, string) error {
		return errors.New("模拟替换失败")
	})
	if err == nil || !strings.Contains(err.Error(), "原子替换敏感信息文件失败") {
		t.Fatalf("应返回原子替换错误：%v", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("读取旧敏感信息文件失败：%v", readErr)
	}
	if string(content) != "old" {
		t.Fatalf("替换失败后旧文件不应被破坏：%q", content)
	}
	assertNoSecretTempFiles(t, directory)
}

func TestStoreSecretFileUsesRestrictedPermissions(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows 文件权限由 ACL 控制")
	}
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	if err := store.Put(t.Context(), "secret://dns/cloudflare/mode", map[string]string{"api_token": "token"}); err != nil {
		t.Fatalf("保存敏感信息失败：%v", err)
	}
	info, err := os.Stat(filepath.Join(directory, "secrets.json"))
	if err != nil {
		t.Fatalf("读取敏感信息文件权限失败：%v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("敏感信息文件权限应为 0600，实际为 %o", info.Mode().Perm())
	}
}

func assertNoSecretTempFiles(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".secrets.json.tmp-*"))
	if err != nil {
		t.Fatalf("检查敏感信息临时文件失败：%v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("不应残留敏感信息临时文件：%v", matches)
	}
}
