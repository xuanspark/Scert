package certificate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func historyTestResource(t *testing.T, names []string, before, after time.Time) Resource {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuerPublic, issuerPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuer := &x509.Certificate{
		SerialNumber: big.NewInt(101), Subject: pkix.Name{CommonName: "历史测试签发机构"},
		NotBefore: before, NotAfter: after, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	issuerDER, err := x509.CreateCertificate(rand.Reader, issuer, issuer, issuerPublic, issuerPrivate)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(102), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: before, NotAfter: after, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, issuer, public, issuerPrivate)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return Resource{
		Domains: names, Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		IssuerCertificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuerDER}),
		PrivateKey:        pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}),
	}
}

func historyTestStore(t *testing.T) FileStore {
	t.Helper()
	root := t.TempDir()
	return FileStore{Root: filepath.Join(root, "certificates"), BackupRoot: filepath.Join(root, "backups")}
}

func historySave(t *testing.T, store FileStore, certificateType string, names []string) SavedResource {
	t.Helper()
	resource := historyTestResource(t, names, time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour))
	saved, err := store.SaveForType(context.Background(), "example.com", certificateType, resource)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func historyRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func historyMetadata(t *testing.T, directory string, change func(*SavedResource)) {
	t.Helper()
	path := filepath.Join(directory, "metadata.json")
	var saved SavedResource
	if err := json.Unmarshal(historyRead(t, path), &saved); err != nil {
		t.Fatal(err)
	}
	change(&saved)
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryRestorePreservesChainAndArchives(t *testing.T) {
	store := historyTestStore(t)
	first := historySave(t, store, "single", []string{"example.com"})
	chain := historyRead(t, first.FullChainPath)
	key := historyRead(t, first.PrivateKeyPath)
	second := historySave(t, store, "single", []string{"example.com"})
	items, err := store.ListVersions(context.Background(), "example.com", "single")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !items[0].Current || items[1].Current || items[1].HistoryID == "" {
		t.Fatalf("历史列表错误：%+v", items)
	}
	selected, resource, err := store.LoadVersion(context.Background(), "example.com", "single", items[1].HistoryID)
	if err != nil {
		t.Fatal(err)
	}
	combined := append(append([]byte(nil), resource.Certificate...), resource.IssuerCertificate...)
	if selected.Version != first.Version || !bytes.Equal(combined, chain) {
		t.Fatal("历史读取必须保留完整链")
	}
	result, err := store.RestoreVersion(context.Background(), "example.com", "single", items[1].HistoryID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version == first.Version || result.Version == second.Version || !result.Current || result.ChainLength != 2 {
		t.Fatalf("恢复必须产生新的当前版本：%+v", result)
	}
	if !bytes.Equal(historyRead(t, result.FullChainPath), chain) || !bytes.Equal(historyRead(t, result.PrivateKeyPath), key) {
		t.Fatal("恢复后完整链或私钥发生变化")
	}
	if bytes.Count(historyRead(t, result.CertificatePath), []byte("BEGIN CERTIFICATE")) != 1 {
		t.Fatal("恢复后的叶子证书文件不应包含完整证书链")
	}
	entries, err := os.ReadDir(filepath.Dir(result.Directory))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != result.Version {
		t.Fatal("正式目录只能保留当前版本")
	}
	items, err = store.ListVersions(context.Background(), "example.com", "single")
	if err != nil || len(items) != 3 {
		t.Fatalf("恢复不得丢失原历史：%+v，%v", items, err)
	}
	if items[0].HistoryID != result.HistoryID || !items[0].Current {
		t.Fatal("恢复后的当前历史标识不稳定")
	}
}

func TestHistoryExpiredAndFutureVersionsRemainVisibleButCannotRestore(t *testing.T) {
	for _, future := range []bool{false, true} {
		name := "已过期"
		if future {
			name = "未生效"
		}
		t.Run(name, func(t *testing.T) {
			store := historyTestStore(t)
			before, after := time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour)
			if future {
				before, after = time.Now().Add(time.Hour), time.Now().Add(48*time.Hour)
			}
			old, err := store.SaveForType(context.Background(), "example.com", "single", historyTestResource(t, []string{"example.com"}, before, after))
			if err != nil {
				t.Fatal(err)
			}
			current := historySave(t, store, "single", []string{"example.com"})
			original := historyRead(t, current.FullChainPath)
			items, err := store.ListVersions(context.Background(), "example.com", "single")
			if err != nil || len(items) != 2 {
				t.Fatalf("不可恢复的历史仍需展示：%v", err)
			}
			if _, err := store.RestoreVersion(context.Background(), "example.com", "single", old.Version); err == nil {
				t.Fatal("不允许恢复有效期外的证书")
			}
			if !bytes.Equal(original, historyRead(t, current.FullChainPath)) {
				t.Fatal("拒绝恢复不应改变当前证书")
			}
		})
	}
}

func TestHistoryRejectsAmbiguousSANVersionAndUsesStableIdentity(t *testing.T) {
	store := historyTestStore(t)
	historySave(t, store, "san", []string{"example.com", "old.example.com"})
	current := historySave(t, store, "san", []string{"example.com", "new.example.com"})
	// 兼容旧版本按不同 SAN 目录独立编号产生的真实历史歧义。
	historyMetadata(t, current.BackupDirectory, func(saved *SavedResource) { saved.Version = current.Version })
	items, err := store.ListVersions(context.Background(), "example.com", "san")
	if err != nil || len(items) != 2 {
		t.Fatalf("读取同号历史失败：%v", err)
	}
	if items[0].HistoryID == items[1].HistoryID {
		t.Fatal("不同 SAN 历史必须具有不同标识")
	}
	if _, _, err := store.LoadVersion(context.Background(), "example.com", "san", current.Version); err == nil || !strings.Contains(err.Error(), "歧义") {
		t.Fatalf("不能默默选取同名版本：%v", err)
	}
	for _, item := range items {
		selected, _, err := store.LoadVersion(context.Background(), "example.com", "san", item.HistoryID)
		if err != nil || selected.CertificateIdentifier != item.CertificateIdentifier {
			t.Fatalf("历史标识未精确选择：%v", err)
		}
	}
}

func TestHistoryMetadataCannotOverrideFilesOrValidity(t *testing.T) {
	store := historyTestStore(t)
	first := historySave(t, store, "single", []string{"example.com"})
	current := historySave(t, store, "single", []string{"example.com"})
	historyMetadata(t, current.BackupDirectory, func(saved *SavedResource) {
		saved.Directory = "/etc"
		saved.PrivateKeyPath = "/etc/shadow"
		saved.BackupDirectory = "/"
		saved.FingerprintSHA256 = "伪造"
		saved.NotAfter = time.Now().Add(999 * 24 * time.Hour)
	})
	saved, _, err := store.LoadVersion(context.Background(), "example.com", "single", first.Version)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Directory != current.BackupDirectory || saved.PrivateKeyPath == "/etc/shadow" || saved.BackupDirectory != "" || saved.FingerprintSHA256 == "伪造" || !saved.NotAfter.Equal(first.NotAfter) {
		t.Fatalf("不应相信归档路径或伪造元数据：%+v", saved)
	}
}

func TestHistoryRejectsInvalidSelectorAndCancellation(t *testing.T) {
	store := historyTestStore(t)
	current := historySave(t, store, "single", []string{"example.com"})
	for _, selector := range []string{"../key.pem", "", "history-xyz", "history-" + strings.Repeat("a", 64), "20260830-V99.0"} {
		if _, err := store.RestoreVersion(context.Background(), "example.com", "single", selector); err == nil {
			t.Fatalf("非法或缺失历史不应恢复：%q", selector)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ListVersions(ctx, "example.com", "single"); !errors.Is(err, context.Canceled) {
		t.Fatalf("未传播取消：%v", err)
	}
	if _, err := store.RestoreVersion(ctx, "example.com", "single", current.Version); !errors.Is(err, context.Canceled) {
		t.Fatalf("未传播取消：%v", err)
	}
}

func TestHistoryRejectsSymlinkArchiveAndFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("本轮只验收 Linux 符号链接安全")
	}
	for _, target := range []string{"archive", "key.pem", "metadata.json"} {
		t.Run(target, func(t *testing.T) {
			store := historyTestStore(t)
			historySave(t, store, "single", []string{"example.com"})
			current := historySave(t, store, "single", []string{"example.com"})
			path := filepath.Join(current.BackupDirectory, target)
			if target == "archive" {
				path = current.BackupDirectory
			}
			moved := path + "-original"
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, path); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ListVersions(context.Background(), "example.com", "single"); err == nil {
				t.Fatal("不允许通过符号链接读取历史证书")
			}
		})
	}
}

func TestServiceHistoryRestoreRejectsChangedSANBeforeWriting(t *testing.T) {
	root := t.TempDir()
	cfg := certificateTestConfig(root)
	store := FileStore{Root: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup}
	first := historySave(t, store, "san", []string{"example.com", "old.example.com"})
	current := historySave(t, store, "san", []string{"example.com", "new.example.com"})
	before := historyRead(t, current.FullChainPath)
	item, err := domain.NewDomainWithOptions("example.com", domain.CertificateSAN, []string{"new.example.com"}, true, domain.ACMEEnvironmentStaging, "cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	repository := &serviceRepository{item: item}
	service := NewService(cfg, repository, nil, nil, nil)
	if _, err := service.RestoreVersion(context.Background(), item.ID, first.Version); err == nil {
		t.Fatal("历史证书必须覆盖当前配置的全部域名")
	}
	if repository.updateCalls != 0 || !bytes.Equal(before, historyRead(t, current.FullChainPath)) {
		t.Fatal("域名预检失败不能修改文件或数据库")
	}
}

func TestHistoryFiltersOtherCertificateTypes(t *testing.T) {
	store := historyTestStore(t)
	historySave(t, store, "single", []string{"example.com"})
	historySave(t, store, "single", []string{"example.com"})
	historySave(t, store, "san", []string{"example.com", "www.example.com"})
	historySave(t, store, "san", []string{"example.com", "api.example.com"})
	for _, certificateType := range []string{"single", "san"} {
		items, err := store.ListVersions(context.Background(), "example.com", certificateType)
		if err != nil || len(items) != 2 {
			t.Fatalf("证书类型历史隔离失败：%s，%v", certificateType, err)
		}
		for _, item := range items {
			if item.CertificateType != certificateType {
				t.Fatal("历史列表混入其他类型")
			}
		}
	}
}
