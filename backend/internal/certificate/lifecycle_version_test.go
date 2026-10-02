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

func TestFileStoreUsesSingleDatedCurrentVersion(t *testing.T) {
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
	if first.Version != "20260827-V1.0" || second.Version != "20260827-V1.1" {
		t.Fatalf("证书版本命名错误：first=%q second=%q", first.Version, second.Version)
	}
	expectedSavedAt := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	if !first.SavedAt.Equal(expectedSavedAt) || !second.SavedAt.Equal(expectedSavedAt) {
		t.Fatalf("saved timestamp mismatch: first=%s second=%s", first.SavedAt, second.SavedAt)
	}
	wantCurrent := filepath.Join(root, "ssl", "example.com", "single", second.Version)
	if second.Directory != wantCurrent {
		t.Fatalf("当前目录错误：got=%q want=%q", second.Directory, wantCurrent)
	}
	if _, err := os.Stat(first.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("旧当前版本目录仍存在：%v", err)
	}
	for _, name := range []string{"cert.pem", "fullchain.pem", "key.pem", "metadata.json"} {
		if _, err := os.Stat(filepath.Join(wantCurrent, name)); err != nil {
			t.Fatalf("当前证书文件不存在：%s：%v", name, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(wantCurrent))
	if err != nil {
		t.Fatalf("读取证书类型目录失败：%v", err)
	}
	if len(entries) != 1 || entries[0].Name() != second.Version {
		t.Fatalf("正式证书目录应只保留一个版本：%v", entries)
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
		t.Fatalf("读取当前证书版本失败：%v", err)
	}
	if len(versions) != 2 || versions[0].Version != second.Version || !versions[0].Current || versions[1].Version != first.Version || versions[1].Current {
		t.Fatalf("当前证书版本列表错误：%+v", versions)
	}
	metadata, loaded, err := store.LoadCurrent(context.Background(), "example.com", "single")
	if err != nil {
		t.Fatalf("读取当前证书失败：%v", err)
	}
	if metadata.Version != second.Version || metadata.Directory != second.Directory || !metadata.SavedAt.Equal(expectedSavedAt) || len(loaded.Certificate) == 0 || len(loaded.PrivateKey) == 0 {
		t.Fatalf("当前证书内容错误：metadata=%+v", metadata)
	}
}

func TestFileStoreListVersionsReturnsEmptyWhenNoCurrentCertificate(t *testing.T) {
	store := FileStore{Root: t.TempDir()}
	versions, err := store.ListVersions(context.Background(), "example.com", "san")
	if err != nil {
		t.Fatalf("无当前证书时查询失败：%v", err)
	}
	if versions == nil || len(versions) != 0 {
		t.Fatalf("无当前证书时应返回空列表：%+v", versions)
	}
}

func TestRollbackSavedResourceRestoresPreviousCertificateInCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	store := FileStore{
		Root:       filepath.Join(root, "ssl"),
		BackupRoot: filepath.Join(root, "backups"),
		Now:        func() time.Time { return time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC) },
	}
	first, err := store.SaveForType(context.Background(), "example.com", "single", testResource(t, "example.com"))
	if err != nil {
		t.Fatalf("保存初始证书失败：%v", err)
	}
	firstCertificate, err := os.ReadFile(first.CertificatePath)
	if err != nil {
		t.Fatalf("读取初始证书失败：%v", err)
	}
	second, err := store.SaveForType(context.Background(), "example.com", "single", testResource(t, "example.com"))
	if err != nil {
		t.Fatalf("保存新证书失败：%v", err)
	}
	if second.BackupDirectory == "" {
		t.Fatal("替换已有证书时应生成备份目录")
	}
	if err := RollbackSavedResource(context.Background(), second); err != nil {
		t.Fatalf("回滚证书失败：%v", err)
	}
	currentCertificate, err := os.ReadFile(second.CertificatePath)
	if err != nil {
		t.Fatalf("回滚后当前证书不存在：%v", err)
	}
	if string(currentCertificate) != string(firstCertificate) {
		t.Fatal("回滚后当前证书内容不匹配")
	}
	current, _, err := store.LoadCurrent(context.Background(), "example.com", "single")
	if err != nil {
		t.Fatalf("读取回滚后的当前证书失败：%v", err)
	}
	if current.Version != first.Version || current.Directory != second.Directory {
		t.Fatalf("回滚后的当前元数据错误：%+v", current)
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
	if _, err := os.Stat(saved.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("回滚后当前版本目录仍存在：%v", err)
	}
}

func TestFileStoreRejectsMissingHistoricalVersion(t *testing.T) {
	store := FileStore{Root: t.TempDir()}
	if _, err := store.RestoreVersion(context.Background(), "example.com", "single", "20260830-V1.0"); err == nil {
		t.Fatal("不存在的历史证书版本不应恢复成功")
	}
}

func TestFileStoreUsesStableSANIdentifierAndRemovesPreviousIdentifier(t *testing.T) {
	root := t.TempDir()
	store := FileStore{
		Root:       filepath.Join(root, "ssl"),
		BackupRoot: filepath.Join(root, "backups"),
		Now:        func() time.Time { return time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC) },
	}
	firstResource := testResource(t, "example.com")
	firstResource.Domains = []string{"example.com", "www.example.com"}
	first, err := store.SaveForType(context.Background(), "example.com", "san", firstResource)
	if err != nil {
		t.Fatalf("save first SAN certificate: %v", err)
	}
	if first.CertificateIdentifier == "" {
		t.Fatal("SAN certificate identifier should be stored")
	}
	if first.Version != "20260830-V1.0" {
		t.Fatalf("unexpected first SAN version: %s", first.Version)
	}
	wantFirst := filepath.Join(root, "ssl", "example.com", "san", first.CertificateIdentifier, first.Version)
	if first.Directory != wantFirst {
		t.Fatalf("unexpected first SAN directory: got=%q want=%q", first.Directory, wantFirst)
	}

	secondResource := firstResource
	secondResource.Domains = []string{"api.example.com", "example.com"}
	second, err := store.SaveForType(context.Background(), "example.com", "san", secondResource)
	if err != nil {
		t.Fatalf("save second SAN certificate: %v", err)
	}
	if second.CertificateIdentifier == first.CertificateIdentifier {
		t.Fatal("different SAN sets should use different identifiers")
	}
	if second.Version != "20260830-V1.1" {
		t.Fatalf("unexpected second SAN version: %s", second.Version)
	}
	if _, err := os.Stat(first.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous SAN certificate directory still exists: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "ssl", "example.com", "san"))
	if err != nil {
		t.Fatalf("read SAN directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != second.CertificateIdentifier {
		t.Fatalf("SAN directory should keep only the active identifier: %v", entries)
	}
}
