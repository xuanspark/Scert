package certificate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFileStoreVersionedLayoutAndHistory(t *testing.T) {
	root := t.TempDir()
	store := FileStore{
		Root:       filepath.Join(root, "ssl"),
		BackupRoot: filepath.Join(root, "backups"),
		Now:        func() time.Time { return time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC) },
	}
	resource := testResource(t, "example.com")
	first, err := store.SaveForType(context.Background(), "example.com", "single", resource)
	if err != nil {
		t.Fatalf("保存第一版证书失败：%v", err)
	}
	second, err := store.SaveForType(context.Background(), "example.com", "single", resource)
	if err != nil {
		t.Fatalf("保存第二版证书失败：%v", err)
	}
	if first.Version == second.Version {
		t.Fatalf("重复保存不应覆盖历史版本：%q", first.Version)
	}
	wantCurrent := filepath.Join(root, "ssl", "example.com", "single", "current")
	if second.Directory != wantCurrent {
		t.Fatalf("当前目录错误：got=%q want=%q", second.Directory, wantCurrent)
	}
	for _, path := range []string{
		filepath.Join(wantCurrent, "cert.pem"),
		filepath.Join(wantCurrent, "fullchain.pem"),
		filepath.Join(wantCurrent, "key.pem"),
		filepath.Join(wantCurrent, "metadata.json"),
		filepath.Join(root, "ssl", "example.com", "single", "versions", first.Version, "metadata.json"),
		filepath.Join(root, "ssl", "example.com", "single", "versions", second.Version, "metadata.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("证书文件不存在：%s：%v", path, err)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(second.PrivateKeyPath)
		if err != nil {
			t.Fatalf("读取私钥权限失败：%v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("私钥权限错误：%o", info.Mode().Perm())
		}
	}
	versions, err := store.ListVersions(context.Background(), "example.com", "single")
	if err != nil {
		t.Fatalf("读取证书版本失败：%v", err)
	}
	if len(versions) != 2 || versions[0].Version != second.Version || versions[1].Version != first.Version {
		t.Fatalf("证书版本历史错误：%+v", versions)
	}
	metadata, loaded, err := store.LoadCurrent(context.Background(), "example.com", "single")
	if err != nil {
		t.Fatalf("读取当前证书失败：%v", err)
	}
	if metadata.Version != second.Version || len(loaded.Certificate) == 0 || len(loaded.PrivateKey) == 0 {
		t.Fatalf("当前证书内容错误：metadata=%+v", metadata)
	}
}

func TestFileStoreListVersionsReturnsEmptyWhenNoHistory(t *testing.T) {
	store := FileStore{Root: t.TempDir()}
	versions, err := store.ListVersions(context.Background(), "example.com", "san")
	if err != nil {
		t.Fatalf("无版本记录时查询失败：%v", err)
	}
	if versions == nil || len(versions) != 0 {
		t.Fatalf("无版本记录时应返回空列表：%+v", versions)
	}
}

func TestRollbackSavedResourceRestoresPreviousCertificate(t *testing.T) {
	root := t.TempDir()
	store := FileStore{
		Root:       filepath.Join(root, "ssl"),
		BackupRoot: filepath.Join(root, "backups"),
		Now:        func() time.Time { return time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC) },
	}
	resource := testResource(t, "example.com")
	first, err := store.SaveForType(context.Background(), "example.com", "single", resource)
	if err != nil {
		t.Fatalf("保存初始证书失败：%v", err)
	}
	second, err := store.SaveForType(context.Background(), "example.com", "single", resource)
	if err != nil {
		t.Fatalf("保存新证书失败：%v", err)
	}
	if second.BackupDirectory == "" {
		t.Fatal("替换已有证书时应生成备份目录")
	}
	if err := RollbackSavedResource(context.Background(), second); err != nil {
		t.Fatalf("回滚证书失败：%v", err)
	}
	if _, err := os.Stat(first.FullChainPath); err != nil {
		t.Fatalf("回滚后当前证书不存在：%v", err)
	}
	if _, err := os.Stat(second.PrivateKeyPath); err != nil {
		t.Fatalf("回滚后当前私钥不存在：%v", err)
	}
}

func TestRollbackSavedResourceClearsNewCertificateFiles(t *testing.T) {
	root := t.TempDir()
	store := FileStore{
		Root:       filepath.Join(root, "ssl"),
		BackupRoot: filepath.Join(root, "backups"),
	}
	saved, err := store.SaveForType(context.Background(), "example.com", "single", testResource(t, "example.com"))
	if err != nil {
		t.Fatalf("保存证书失败：%v", err)
	}
	if err := RollbackSavedResource(context.Background(), saved); err != nil {
		t.Fatalf("清理失败证书失败：%v", err)
	}
	for _, name := range []string{"cert.pem", "fullchain.pem", "key.pem", "metadata.json"} {
		if _, err := os.Stat(filepath.Join(saved.Directory, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("回滚后文件仍存在：%s，错误：%v", name, err)
		}
	}
}
