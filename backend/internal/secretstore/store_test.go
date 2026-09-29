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

func TestStoreListsMetadataAndDeletesReferenceWithoutLeakingValues(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	if err := store.Put(t.Context(), "secret://ssh/key-b", map[string]string{"private_key": "secret-b"}); err != nil {
		t.Fatalf("保存第二个 SSH 私钥失败：%v", err)
	}
	if err := store.Put(t.Context(), "secret://dns/provider", map[string]string{"token": "dns-secret"}); err != nil {
		t.Fatalf("保存 DNS 凭据失败：%v", err)
	}
	if err := store.Put(t.Context(), "secret://ssh/key-a", map[string]string{"private_key": "secret-a"}); err != nil {
		t.Fatalf("保存第一个 SSH 私钥失败：%v", err)
	}

	items, err := store.List(t.Context(), "secret://ssh/")
	if err != nil {
		t.Fatalf("列出 SSH 私钥引用失败：%v", err)
	}
	if len(items) != 2 || items[0].Reference != "secret://ssh/key-a" || items[1].Reference != "secret://ssh/key-b" {
		t.Fatalf("SSH 私钥引用列表不正确：%+v", items)
	}
	if err := store.Delete(t.Context(), items[0].Reference); err != nil {
		t.Fatalf("删除 SSH 私钥引用失败：%v", err)
	}
	exists, err := store.Has(t.Context(), items[0].Reference)
	if err != nil || exists {
		t.Fatalf("SSH 私钥引用删除后仍然存在：exists=%t err=%v", exists, err)
	}
	if err := store.Delete(t.Context(), items[0].Reference); err == nil {
		t.Fatal("重复删除不存在的敏感信息引用应返回错误")
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

func TestStoreDisablesAndEnablesValue(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	reference := "secret://dns/cloudflare/lifecycle"
	value := map[string]string{"api_token": "lifecycle-token"}
	if err := store.Put(t.Context(), reference, value); err != nil {
		t.Fatalf("put secret: %v", err)
	}
	if err := store.Disable(t.Context(), reference); err != nil {
		t.Fatalf("disable secret: %v", err)
	}
	if _, err := store.Get(t.Context(), reference); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled secret should not be readable: %v", err)
	}
	items, err := store.List(t.Context(), reference)
	if err != nil {
		t.Fatalf("list metadata: %v", err)
	}
	if len(items) != 1 || items[0].Status != StatusDisabled {
		t.Fatalf("disabled status was not persisted: %+v", items)
	}
	if err := store.Enable(t.Context(), reference); err != nil {
		t.Fatalf("enable secret: %v", err)
	}
	loaded, err := store.Get(t.Context(), reference)
	if err != nil {
		t.Fatalf("read enabled secret: %v", err)
	}
	if loaded["api_token"] != value["api_token"] {
		t.Fatalf("enabled secret value mismatch: %v", loaded)
	}
}

func TestStoreRejectsMissingMasterKey(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Put(t.Context(), "secret://dns/cloudflare/missing-key", map[string]string{"api_token": "token"}); err != nil {
		t.Fatalf("put secret: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, "master.key")); err != nil {
		t.Fatalf("remove test key: %v", err)
	}
	if _, err := Open(directory); err == nil {
		t.Fatal("store must reject missing key when secrets exist")
	}
}

func TestStoreRotatesMasterKeyAndPreservesValues(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	reference := "secret://dns/cloudflare/rotated"
	if err := store.Put(t.Context(), reference, map[string]string{"api_token": "rotated-token"}); err != nil {
		t.Fatalf("put secret: %v", err)
	}
	if err := store.Disable(t.Context(), reference); err != nil {
		t.Fatalf("disable secret: %v", err)
	}
	oldKey, err := os.ReadFile(filepath.Join(directory, "master.key"))
	if err != nil {
		t.Fatalf("read old key: %v", err)
	}
	if err := store.RotateMasterKey(t.Context()); err != nil {
		t.Fatalf("rotate key: %v", err)
	}
	newKey, err := os.ReadFile(filepath.Join(directory, "master.key"))
	if err != nil {
		t.Fatalf("read new key: %v", err)
	}
	if string(oldKey) == string(newKey) {
		t.Fatal("rotated key must differ from old key")
	}
	if _, err := store.Get(t.Context(), reference); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled status was not preserved after rotation: %v", err)
	}
	if err := store.Enable(t.Context(), reference); err != nil {
		t.Fatalf("enable rotated secret: %v", err)
	}
	reopened, err := Open(directory)
	if err != nil {
		t.Fatalf("reopen with new key: %v", err)
	}
	loaded, err := reopened.Get(t.Context(), reference)
	if err != nil {
		t.Fatalf("read with new key: %v", err)
	}
	if loaded["api_token"] != "rotated-token" {
		t.Fatalf("rotated secret value mismatch: %v", loaded)
	}
}
