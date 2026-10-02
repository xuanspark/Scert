package cli

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

func TestCreateBackupIncludesSecretsCertificatesAndSQLiteSnapshot(t *testing.T) {
	base := t.TempDir()
	cfg := isolatedCLIConfig(base)
	for _, directory := range []string{cfg.Paths.Config, cfg.Paths.Data, cfg.Paths.Certificate, cfg.Paths.Backup} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("创建测试目录失败：%v", err)
		}
	}
	configPath := filepath.Join(cfg.Paths.Config, "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	if err := createTestSecretStore(cfg.Paths.Config); err != nil {
		t.Fatalf("创建敏感配置失败：%v", err)
	}
	versionDirectory := filepath.Join(cfg.Paths.Certificate, "example.com", "20260831-V1.0")
	if err := os.MkdirAll(versionDirectory, 0o750); err != nil {
		t.Fatalf("创建证书版本目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDirectory, "fullchain.pem"), []byte("certificate"), 0o644); err != nil {
		t.Fatalf("写入证书失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDirectory, "privkey.pem"), []byte("private-key"), 0o600); err != nil {
		t.Fatalf("写入私钥失败：%v", err)
	}

	db, err := database.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatalf("打开测试数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatalf("迁移测试数据库失败：%v", err)
	}
	if _, err := db.DB.Exec(`INSERT INTO audit_logs (action, resource_type, resource_id, status, message, created_at) VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, "backup_test", "test", "1", "success", "备份测试"); err != nil {
		t.Fatalf("写入测试数据失败：%v", err)
	}

	output := filepath.Join(base, "backup-output")
	if err := createBackup(context.Background(), cfg, configPath, output); err != nil {
		t.Fatalf("创建完整备份失败：%v", err)
	}
	if err := validateBackupManifest(output); err != nil {
		t.Fatalf("备份清单校验失败：%v", err)
	}
	for _, relative := range []string{
		"scert.json",
		"master.key",
		"secrets.json",
		"scert.db",
		"manifest.json",
		filepath.Join("certificates", "example.com", "20260831-V1.0", "fullchain.pem"),
		filepath.Join("certificates", "example.com", "20260831-V1.0", "privkey.pem"),
	} {
		if _, err := os.Stat(filepath.Join(output, relative)); err != nil {
			t.Fatalf("完整备份缺少文件 %s：%v", relative, err)
		}
	}

	snapshot, err := sql.Open("sqlite", filepath.Join(output, "scert.db"))
	if err != nil {
		t.Fatalf("打开 SQLite 快照失败：%v", err)
	}
	defer snapshot.Close()
	var count int
	if err := snapshot.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'backup_test'`).Scan(&count); err != nil {
		t.Fatalf("读取 SQLite 快照失败：%v", err)
	}
	if count != 1 {
		t.Fatalf("SQLite 快照缺少备份时数据：%d", count)
	}
	if runtime.GOOS != "windows" {
		for _, relative := range []string{"master.key", "secrets.json", "scert.db", "manifest.json"} {
			info, err := os.Stat(filepath.Join(output, relative))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("敏感备份文件权限不正确 %s：%o", relative, info.Mode().Perm())
			}
		}
	}
}

func TestBackupManifestRejectsTampering(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "scert.json"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBackupManifest(directory); err != nil {
		t.Fatalf("写入备份清单失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "scert.json"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBackupManifest(directory); err == nil || !strings.Contains(err.Error(), "校验失败") {
		t.Fatalf("被篡改的备份未被拒绝：%v", err)
	}
}

func TestBackupManifestIsRequired(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "scert.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBackupManifest(directory); err == nil || !strings.Contains(err.Error(), "缺少 manifest.json") {
		t.Fatalf("缺少清单的备份未被拒绝：%v", err)
	}
}

func TestCreateBackupDoesNotChangeExistingParentPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不提供 POSIX 目录权限语义")
	}
	base := t.TempDir()
	parent := filepath.Join(base, "shared")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := isolatedCLIConfig(filepath.Join(base, "scert"))
	for _, directory := range []string{cfg.Paths.Config, cfg.Paths.Data} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(cfg.Paths.Config, "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}
	if err := createSQLiteTestDatabase(cfg.Database.DSN, "parent_mode"); err != nil {
		t.Fatal(err)
	}
	if err := createBackup(context.Background(), cfg, configPath, filepath.Join(parent, "backup")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("备份操作修改了既有父目录权限：%o", info.Mode().Perm())
	}
}

func TestCreateBackupRequiresSecretsWhenInitialized(t *testing.T) {
	base := t.TempDir()
	cfg := isolatedCLIConfig(base)
	for _, directory := range []string{cfg.Paths.Config, cfg.Paths.Data} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(cfg.Paths.Config, "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}
	if err := createSQLiteTestDatabase(cfg.Database.DSN, "missing_secrets"); err != nil {
		t.Fatal(err)
	}
	cfg.SetupReady = true
	if err := createBackup(context.Background(), cfg, configPath, filepath.Join(base, "backup")); err == nil || !strings.Contains(err.Error(), "缺少 master.key") {
		t.Fatalf("已初始化系统缺少敏感文件时仍创建了备份：%v", err)
	}
}

func TestRestoreAdditionalAssetsPreservesCertificateRoot(t *testing.T) {
	base := t.TempDir()
	cfg := isolatedCLIConfig(filepath.Join(base, "target"))
	input := filepath.Join(base, "backup")
	if err := os.MkdirAll(filepath.Join(input, "certificates", "example.com"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Paths.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Paths.Certificate, "old.example.com"), 0o750); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{
		filepath.Join(input, "master.key"):                                  "new-master",
		filepath.Join(input, "secrets.json"):                                "new-secrets",
		filepath.Join(input, "certificates", "example.com", "privkey.pem"):  "new-private-key",
		filepath.Join(cfg.Paths.Config, "master.key"):                       "old-master",
		filepath.Join(cfg.Paths.Config, "secrets.json"):                     "old-secrets",
		filepath.Join(cfg.Paths.Certificate, "old.example.com", "cert.pem"): "old-certificate",
	} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatalf("写入测试恢复文件失败：%v", err)
		}
	}

	if err := restoreAdditionalAssets(cfg, input); err != nil {
		t.Fatalf("恢复敏感配置和证书失败：%v", err)
	}
	if info, err := os.Stat(cfg.Paths.Certificate); err != nil || !info.IsDir() {
		t.Fatalf("恢复缺失状态失败：%v", err)
	}
	for path, expected := range map[string]string{
		filepath.Join(cfg.Paths.Config, "master.key"):                      "new-master",
		filepath.Join(cfg.Paths.Config, "secrets.json"):                    "new-secrets",
		filepath.Join(cfg.Paths.Certificate, "example.com", "privkey.pem"): "new-private-key",
	} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("恢复结果不正确 %s：data=%q err=%v", path, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.Certificate, "old.example.com")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("备份中不存在的资产仍保留在当前路径：%s err=%v", filepath.Join(cfg.Paths.Certificate, "old.example.com"), err)
	}
	for _, pattern := range []string{
		filepath.Join(cfg.Paths.Config, "master.key.before-restore-*"),
		filepath.Join(cfg.Paths.Config, "secrets.json.before-restore-*"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) != 1 {
			t.Fatalf("恢复前副本不正确 %s：matches=%v err=%v", pattern, matches, err)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(cfg.Paths.Certificate, ".scert-restore-*")); err != nil || len(matches) != 0 {
		t.Fatalf("恢复前副本不正确 %s：matches=%v err=%v", filepath.Join(cfg.Paths.Certificate, ".scert-restore-*"), matches, err)
	}
}

func TestRestoreAdditionalAssetsRestoresAbsentState(t *testing.T) {
	base := t.TempDir()
	cfg := isolatedCLIConfig(filepath.Join(base, "target"))
	input := filepath.Join(base, "backup")
	if err := os.MkdirAll(input, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Paths.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Paths.Certificate, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(cfg.Paths.Config, "master.key"),
		filepath.Join(cfg.Paths.Config, "secrets.json"),
		filepath.Join(cfg.Paths.Certificate, "current.pem"),
	} {
		if err := os.WriteFile(path, []byte("current"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := restoreAdditionalAssets(cfg, input); err != nil {
		t.Fatalf("恢复缺失状态失败：%v", err)
	}
	for _, path := range []string{
		filepath.Join(cfg.Paths.Config, "master.key"),
		filepath.Join(cfg.Paths.Config, "secrets.json"),
		filepath.Join(cfg.Paths.Certificate, "current.pem"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("备份中不存在的资产仍保留在当前路径：%s err=%v", path, err)
		}
	}
	if info, err := os.Stat(cfg.Paths.Certificate); err != nil || !info.IsDir() {
		t.Fatalf("恢复缺失状态失败：%v", err)
	}
	for _, pattern := range []string{
		filepath.Join(cfg.Paths.Config, "master.key.before-restore-*"),
		filepath.Join(cfg.Paths.Config, "secrets.json.before-restore-*"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) != 1 {
			t.Fatalf("缺失状态恢复未保留恢复前副本 %s：matches=%v err=%v", pattern, matches, err)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(cfg.Paths.Certificate, ".scert-restore-*")); err != nil || len(matches) != 0 {
		t.Fatalf("恢复前副本不正确 %s：matches=%v err=%v", filepath.Join(cfg.Paths.Certificate, ".scert-restore-*"), matches, err)
	}
}

func TestRestoreDirectoryWorksWhenCertificateParentIsNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不提供 POSIX 目录权限语义")
	}
	base := t.TempDir()
	parent := filepath.Join(base, "certificate-parent")
	target := filepath.Join(parent, "ssl")
	source := filepath.Join(base, "restore-source")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "old.pem"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "new.pem"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0o755)

	if _, err := restoreDirectoryWithRollback(source, target, "permission"); err != nil {
		t.Fatalf("恢复敏感配置和证书失败：%v", err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "new.pem")); err != nil || string(data) != "new" {
		t.Fatalf("恢复结果不正确 %s：data=%q err=%v", filepath.Join(target, "new.pem"), data, err)
	}
	if _, err := os.Stat(filepath.Join(target, "old.pem")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("恢复缺失状态失败：%v", err)
	}
	if matches, err := filepath.Glob(filepath.Join(target, ".scert-restore-*")); err != nil || len(matches) != 0 {
		t.Fatalf("恢复缺失状态失败：%v", err)
	}
}

func TestValidateRestoreCompatibilityRejectsCrossDatabase(t *testing.T) {
	current := isolatedCLIConfig(t.TempDir())
	backup := current
	backup.Database.DSN = filepath.Join(t.TempDir(), "other.db")
	if err := validateRestoreCompatibility(current, backup); err == nil || !strings.Contains(err.Error(), "跨数据库") {
		t.Fatalf("跨数据库恢复未被拒绝：%v", err)
	}
}

func TestRestoreCommandRestoresCompleteSQLiteBackup(t *testing.T) {
	cfg, currentPath, input := prepareRestoreCommandFixture(t, false)
	expectedMaster, err := os.ReadFile(filepath.Join(input, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	expectedSecrets, err := os.ReadFile(filepath.Join(input, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	expectedConfig, err := os.ReadFile(filepath.Join(input, "scert.json"))
	if err != nil {
		t.Fatal(err)
	}
	previousConfigPath := configPath
	configPath = currentPath
	defer func() { configPath = previousConfigPath }()

	command := newRestoreCommand()
	var output strings.Builder
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--input", input})
	if err := command.Execute(); err != nil {
		t.Fatalf("执行完整恢复失败：%v\n%s", err, output.String())
	}
	restored, err := config.Load(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Server.Port != 9005 {
		t.Fatalf("配置未恢复：%d", restored.Server.Port)
	}
	actualConfig, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(actualConfig) != string(expectedConfig) {
		t.Fatal("恢复后的配置文件与备份内容不一致")
	}
	for path, expected := range map[string][]byte{
		filepath.Join(cfg.Paths.Config, "master.key"):                   expectedMaster,
		filepath.Join(cfg.Paths.Config, "secrets.json"):                 expectedSecrets,
		filepath.Join(cfg.Paths.Certificate, "example.com", "cert.pem"): []byte("new-certificate"),
	} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != string(expected) {
			t.Fatalf("恢复文件不正确 %s：data=%q err=%v", path, data, err)
		}
	}
	if count, err := auditActionCount(cfg.Database.DSN, "restored"); err != nil || count != 1 {
		t.Fatalf("SQLite 数据未恢复：count=%d err=%v", count, err)
	}
	if count, err := auditActionCount(cfg.Database.DSN, "current"); err != nil || count != 0 {
		t.Fatalf("恢复后仍存在当前数据：count=%d err=%v", count, err)
	}
}

func TestRestoreCommandRollsBackCoreAndSecretsOnCertificateFailure(t *testing.T) {
	cfg, currentPath, input := prepareRestoreCommandFixture(t, true)
	previousConfigPath := configPath
	configPath = currentPath
	defer func() { configPath = previousConfigPath }()

	command := newRestoreCommand()
	var output strings.Builder
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--input", input})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "证书恢复目标必须是普通目录") {
		t.Fatalf("证书恢复失败未触发：err=%v output=%s", err, output.String())
	}
	restored, loadErr := config.Load(currentPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if restored.Server.Port != 9004 {
		t.Fatalf("失败后配置未回滚：%d", restored.Server.Port)
	}
	master, readErr := os.ReadFile(filepath.Join(cfg.Paths.Config, "master.key"))
	if readErr != nil || string(master) != "old-master" {
		t.Fatalf("失败后主密钥未回滚：data=%q err=%v", master, readErr)
	}
	certificateTarget, readErr := os.ReadFile(cfg.Paths.Certificate)
	if readErr != nil || string(certificateTarget) != "blocked-certificate-target" {
		t.Fatalf("失败后证书目标被破坏：data=%q err=%v", certificateTarget, readErr)
	}
	if count, countErr := auditActionCount(cfg.Database.DSN, "current"); countErr != nil || count != 1 {
		t.Fatalf("失败后 SQLite 数据未回滚：count=%d err=%v", count, countErr)
	}
	if count, countErr := auditActionCount(cfg.Database.DSN, "restored"); countErr != nil || count != 0 {
		t.Fatalf("失败后仍残留恢复数据：count=%d err=%v", count, countErr)
	}
}

func TestSQLiteDatabasePathSupportsFileURI(t *testing.T) {
	path, memory, err := sqliteDatabasePath("file:/var/lib/scert/scert.db?_pragma=busy_timeout(5000)")
	if err != nil || memory {
		t.Fatalf("SQLite 文件 URI 解析失败：path=%s memory=%t err=%v", path, memory, err)
	}
	if filepath.ToSlash(path) != "/var/lib/scert/scert.db" {
		t.Fatalf("SQLite 文件 URI 路径不正确：%s", path)
	}
	if _, memory, err := sqliteDatabasePath("file::memory:?cache=shared"); err != nil || !memory {
		t.Fatalf("SQLite 内存 URI 识别失败：memory=%t err=%v", memory, err)
	}
}

func createSQLiteTestDatabase(path, action string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db, "sqlite"); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO audit_logs (action, resource_type, resource_id, status, message, created_at) VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, action, "test", "1", "success", action)
	return err
}

func prepareRestoreCommandFixture(t *testing.T, blockedCertificate bool) (config.Config, string, string) {
	t.Helper()
	base := t.TempDir()
	cfg := isolatedCLIConfig(filepath.Join(base, "target"))
	for _, directory := range []string{cfg.Paths.Config, cfg.Paths.Data, cfg.Paths.Backup} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if blockedCertificate {
		if err := os.MkdirAll(filepath.Dir(cfg.Paths.Certificate), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg.Paths.Certificate, []byte("blocked-certificate-target"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.MkdirAll(filepath.Join(cfg.Paths.Certificate, "old.example.com"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg.Paths.Certificate, "old.example.com", "cert.pem"), []byte("old-certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Server.Port = 9004
	currentPath := filepath.Join(cfg.Paths.Config, "scert.json")
	if err := cfg.Save(currentPath); err != nil {
		t.Fatal(err)
	}
	if err := createSQLiteTestDatabase(cfg.Database.DSN, "current"); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{
		filepath.Join(cfg.Paths.Config, "master.key"):   "old-master",
		filepath.Join(cfg.Paths.Config, "secrets.json"): "old-secrets",
	} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	input := filepath.Join(base, "restore-input")
	if err := os.MkdirAll(filepath.Join(input, "certificates", "example.com"), 0o700); err != nil {
		t.Fatal(err)
	}
	backupConfig := cfg
	backupConfig.Server.Port = 9005
	if err := backupConfig.Save(filepath.Join(input, "scert.json")); err != nil {
		t.Fatal(err)
	}
	if err := createSQLiteTestDatabase(filepath.Join(input, "scert.db"), "restored"); err != nil {
		t.Fatal(err)
	}
	if err := createTestSecretStore(input); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "certificates", "example.com", "cert.pem"), []byte("new-certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBackupManifest(input); err != nil {
		t.Fatal(err)
	}
	return cfg, currentPath, input
}

func auditActionCount(path, action string) (int, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = ?`, action).Scan(&count)
	return count, err
}

func createTestSecretStore(directory string) error {
	store, err := secretstore.Open(directory)
	if err != nil {
		return err
	}
	return store.Put(context.Background(), "secret://test/backup", map[string]string{"token": "encrypted-test-value"})
}
