package cli

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

const backupManifestVersion = 1

type backupManifest struct {
	Version   int                  `json:"version"`
	CreatedAt time.Time            `json:"created_at"`
	Files     []backupManifestFile `json:"files"`
}

type backupManifestFile struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func createBackup(ctx context.Context, cfg config.Config, configPath, output string) error {
	output = filepath.Clean(output)
	if err := ensureBackupDestination(output); err != nil {
		return err
	}
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("创建备份父目录失败：%w", err)
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("检查备份父目录失败：%w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return errors.New("备份父路径必须是普通目录")
	}
	staging, err := os.MkdirTemp(parent, ".scert-backup-")
	if err != nil {
		return fmt.Errorf("创建备份暂存目录失败：%w", err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := os.Chmod(staging, 0o700); err != nil {
		return fmt.Errorf("设置备份暂存目录权限失败：%w", err)
	}

	if err := copyFileExclusive(configPath, filepath.Join(staging, "scert.json"), 0o600); err != nil {
		return err
	}
	if err := validateBackupSecretStore(ctx, cfg.Paths.Config, cfg); err != nil {
		return err
	}
	for _, name := range []string{"master.key", "secrets.json"} {
		source := filepath.Join(cfg.Paths.Config, name)
		info, statErr := os.Lstat(source)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return fmt.Errorf("敏感配置文件不是普通文件：%s", name)
			}
			if err := copyFileExclusive(source, filepath.Join(staging, name), 0o600); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("检查敏感配置文件失败：%w", statErr)
		}
		if cfg.SetupReady {
			return fmt.Errorf("已初始化配置缺少敏感配置文件：%s", name)
		}
	}

	if cfg.Database.Driver == "sqlite" {
		_, memory, err := sqliteDatabasePath(cfg.Database.DSN)
		if err != nil {
			return err
		}
		if !memory {
			if err := backupSQLiteDatabase(ctx, cfg, filepath.Join(staging, "scert.db")); err != nil {
				return err
			}
		}
	}
	if cfg.Database.Driver == "mysql" {
		if err := backupMySQL(ctx, cfg, filepath.Join(staging, "scert.sql")); err != nil {
			return err
		}
	}

	if info, err := os.Lstat(cfg.Paths.Certificate); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("证书根目录必须是普通目录，不能使用符号链接")
		}
		if pathsOverlap(cfg.Paths.Certificate, output) {
			return errors.New("备份目录不能位于证书根目录内，也不能包含证书根目录")
		}
		if err := copyDirectoryExclusive(cfg.Paths.Certificate, filepath.Join(staging, "certificates")); err != nil {
			return fmt.Errorf("备份证书目录失败：%w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查证书根目录失败：%w", err)
	}

	if err := writeBackupManifest(staging); err != nil {
		return err
	}
	if err := syncDirectory(staging); err != nil {
		return fmt.Errorf("同步备份暂存目录失败：%w", err)
	}
	if err := os.Rename(staging, output); err != nil {
		return fmt.Errorf("发布备份目录失败：%w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("同步备份父目录失败：%w", err)
	}
	completed = true
	return nil
}

func backupSQLiteDatabase(ctx context.Context, cfg config.Config, output string) error {
	if _, memory, err := sqliteDatabasePath(cfg.Database.DSN); err != nil {
		return err
	} else if memory {
		return errors.New("内存 SQLite 数据库不能创建文件备份")
	}
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("打开 SQLite 数据库失败：%w", err)
	}
	defer db.Close()
	absOutput, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("解析 SQLite 备份路径失败：%w", err)
	}
	if _, err := os.Lstat(absOutput); err == nil {
		return errors.New("SQLite 备份文件已存在")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查 SQLite 备份文件失败：%w", err)
	}
	statement := "VACUUM INTO '" + strings.ReplaceAll(filepath.ToSlash(absOutput), "'", "''") + "'"
	if _, err := db.DB.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("创建 SQLite 一致性快照失败：%w", err)
	}
	if err := os.Chmod(absOutput, 0o600); err != nil {
		return fmt.Errorf("设置 SQLite 备份权限失败：%w", err)
	}
	if err := validateSQLiteDatabase(ctx, absOutput); err != nil {
		return err
	}
	return nil
}

func validateSQLiteDatabase(ctx context.Context, path string) error {
	if !isRegularFile(path) {
		return errors.New("SQLite 备份不是普通文件")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("打开 SQLite 备份失败：%w", err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("检查 SQLite 备份完整性失败：%w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return fmt.Errorf("SQLite 备份完整性检查失败：%s", result)
	}
	return nil
}

func sqliteDatabasePath(dsn string) (string, bool, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == ":memory:" || strings.HasPrefix(dsn, "file::memory:") {
		return "", true, nil
	}
	if strings.HasPrefix(dsn, "file:") {
		dsn = strings.TrimPrefix(strings.SplitN(dsn, "?", 2)[0], "file:")
	}
	if dsn == "" {
		return "", false, errors.New("SQLite 数据库路径不能为空")
	}
	return filepath.Clean(filepath.FromSlash(dsn)), false, nil
}

func validateRestoreCompatibility(current, backup config.Config) error {
	if current.Database.Driver != backup.Database.Driver {
		return fmt.Errorf("备份数据库类型（%s）与当前配置数据库类型（%s）不一致，拒绝恢复", backup.Database.Driver, current.Database.Driver)
	}
	for name, values := range map[string][2]string{
		"配置目录": {current.Paths.Config, backup.Paths.Config},
		"数据目录": {current.Paths.Data, backup.Paths.Data},
		"证书目录": {current.Paths.Certificate, backup.Paths.Certificate},
		"备份目录": {current.Paths.Backup, backup.Paths.Backup},
	} {
		if !samePath(values[0], values[1]) {
			return fmt.Errorf("备份%s与当前配置不一致，拒绝跨目录恢复", name)
		}
	}
	if backup.Database.Driver == "sqlite" {
		currentPath, currentMemory, err := sqliteDatabasePath(current.Database.DSN)
		if err != nil {
			return fmt.Errorf("解析当前 SQLite 数据库路径失败：%w", err)
		}
		backupPath, backupMemory, err := sqliteDatabasePath(backup.Database.DSN)
		if err != nil {
			return fmt.Errorf("解析备份 SQLite 数据库路径失败：%w", err)
		}
		if currentMemory != backupMemory || (!currentMemory && !samePath(currentPath, backupPath)) {
			return errors.New("备份 SQLite 数据库路径与当前配置不一致，拒绝跨数据库恢复")
		}
	}
	if backup.Database.Driver == "mysql" && strings.TrimSpace(current.Database.DSN) != strings.TrimSpace(backup.Database.DSN) {
		return errors.New("备份 MySQL 连接配置与当前配置不一致，拒绝跨数据库恢复")
	}
	return nil
}

func validateBackupSecretStore(ctx context.Context, directory string, cfg config.Config) error {
	masterPath := filepath.Join(directory, "master.key")
	secretsPath := filepath.Join(directory, "secrets.json")
	masterExists := isRegularFile(masterPath)
	secretsExists := isRegularFile(secretsPath)
	if !masterExists {
		if secretsExists {
			return errors.New("敏感配置包含 secrets.json，但缺少 master.key")
		}
		if cfg.SetupReady {
			return errors.New("已初始化配置缺少 master.key")
		}
		return nil
	}
	store, err := secretstore.Open(directory)
	if err != nil {
		return fmt.Errorf("校验敏感配置主密钥失败：%w", err)
	}
	metadata, err := store.List(ctx, "")
	if err != nil {
		return fmt.Errorf("校验敏感配置文件失败：%w", err)
	}
	available := make(map[string]struct{}, len(metadata))
	for _, item := range metadata {
		if _, err := store.Get(ctx, item.Reference); err != nil {
			return fmt.Errorf("校验敏感配置引用失败：%s：%w", item.Reference, err)
		}
		available[item.Reference] = struct{}{}
	}
	for _, reference := range []string{cfg.DNS.CredentialRef, cfg.Notification.HTTPCredentialRef, cfg.Notification.SMTPCredentialRef} {
		reference = strings.TrimSpace(reference)
		if reference == "" {
			continue
		}
		if _, exists := available[reference]; !exists {
			return fmt.Errorf("敏感配置缺少配置引用：%s", reference)
		}
	}
	if cfg.SetupReady && !secretsExists {
		return errors.New("已初始化配置缺少 secrets.json")
	}
	return nil
}

func restoreAdditionalAssets(cfg config.Config, input string) error {
	timestamp := time.Now().UTC().Format("20060102-150405.000000000")
	rollbacks := make([]func() error, 0, 3)
	rollbackAll := func() error {
		var rollbackErr error
		for index := len(rollbacks) - 1; index >= 0; index-- {
			rollbackErr = errors.Join(rollbackErr, rollbacks[index]())
		}
		return rollbackErr
	}
	for _, name := range []string{"master.key", "secrets.json"} {
		source := filepath.Join(input, name)
		target := filepath.Join(cfg.Paths.Config, name)
		info, err := os.Lstat(source)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return errors.Join(fmt.Errorf("备份敏感配置文件不是普通文件：%s", name), rollbackAll())
			}
			rollback, restoreErr := restoreFileWithRollback(source, target, 0o600, timestamp)
			if restoreErr != nil {
				return errors.Join(restoreErr, rollbackAll())
			}
			rollbacks = append(rollbacks, rollback)
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errors.Join(fmt.Errorf("检查备份敏感配置文件失败：%w", err), rollbackAll())
		}
		rollback, removeErr := removeFileWithRollback(target, 0o600, timestamp)
		if removeErr != nil {
			return errors.Join(removeErr, rollbackAll())
		}
		rollbacks = append(rollbacks, rollback)
	}
	certificateSource := filepath.Join(input, "certificates")
	if info, err := os.Lstat(certificateSource); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.Join(errors.New("备份证书路径必须是普通目录"), rollbackAll())
		}
		rollback, err := restoreDirectoryWithRollback(certificateSource, cfg.Paths.Certificate, timestamp)
		if err != nil {
			return errors.Join(err, rollbackAll())
		}
		rollbacks = append(rollbacks, rollback)
	} else if errors.Is(err, os.ErrNotExist) {
		rollback, removeErr := removeDirectoryWithRollback(cfg.Paths.Certificate, timestamp)
		if removeErr != nil {
			return errors.Join(removeErr, rollbackAll())
		}
		rollbacks = append(rollbacks, rollback)
	} else {
		return errors.Join(fmt.Errorf("检查备份证书目录失败：%w", err), rollbackAll())
	}
	return nil
}

func restoreFileWithRollback(source, target string, mode os.FileMode, timestamp string) (func() error, error) {
	if !isRegularFile(source) {
		return nil, errors.New("恢复源文件必须是普通文件")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return nil, fmt.Errorf("创建恢复目标目录失败：%w", err)
	}
	backupPath := ""
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("恢复目标文件不是普通文件")
		}
		backupPath = target + ".before-restore-" + timestamp
		if err := copyFileExclusive(target, backupPath, mode); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("检查恢复目标文件失败：%w", err)
	}
	if err := copyFile(source, target, mode); err != nil {
		if backupPath != "" {
			_ = copyFile(backupPath, target, mode)
		}
		return nil, err
	}
	return func() error {
		if backupPath != "" {
			return copyFile(backupPath, target, mode)
		}
		return os.Remove(target)
	}, nil
}

func removeFileWithRollback(target string, mode os.FileMode, timestamp string) (func() error, error) {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return func() error { return nil }, nil
	}
	if err != nil {
		return nil, fmt.Errorf("检查恢复目标文件失败：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("恢复目标文件不是普通文件")
	}
	backupPath := target + ".before-restore-" + timestamp
	if err := copyFileExclusive(target, backupPath, mode); err != nil {
		return nil, err
	}
	if err := os.Remove(target); err != nil {
		return nil, fmt.Errorf("移除备份中不存在的当前文件失败：%w", err)
	}
	return func() error { return copyFile(backupPath, target, mode) }, nil
}

func removeDirectoryWithRollback(target, timestamp string) (func() error, error) {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return func() error { return nil }, nil
	}
	if err != nil {
		return nil, fmt.Errorf("检查证书恢复目标失败：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("证书恢复目标必须是普通目录")
	}
	return restoreDirectoryContentsWithRollback("", target, timestamp)
}

func restoreDirectoryWithRollback(source, target, timestamp string) (func() error, error) {
	if pathsOverlap(source, target) {
		return nil, errors.New("证书恢复源目录和目标目录不能互相包含")
	}
	return restoreDirectoryContentsWithRollback(source, target, timestamp)
}

func restoreDirectoryContentsWithRollback(source, target, timestamp string) (func() error, error) {
	createdTarget := false
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, errors.New("证书恢复目标必须是普通目录")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return nil, fmt.Errorf("创建证书恢复父目录失败：%w", err)
		}
		if err := os.Mkdir(target, 0o750); err != nil {
			return nil, fmt.Errorf("创建证书恢复父目录失败：%w", err)
		}
		createdTarget = true
	} else {
		return nil, fmt.Errorf("检查证书恢复目标失败：%w", err)
	}

	transaction, err := os.MkdirTemp(target, ".scert-restore-")
	if err != nil {
		return nil, fmt.Errorf("保留现有证书目录失败：%w", err)
	}
	oldDirectory := filepath.Join(transaction, "old")
	if err := os.Mkdir(oldDirectory, 0o700); err != nil {
		_ = os.RemoveAll(transaction)
		return nil, fmt.Errorf("保留现有证书目录失败：%w", err)
	}

	rollback := func() error {
		var rollbackErr error
		rollbackErr = errors.Join(rollbackErr, clearDirectoryChildren(target, filepath.Base(transaction)))
		rollbackErr = errors.Join(rollbackErr, moveDirectoryChildren(oldDirectory, target))
		if createdTarget {
			if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, removeErr)
			}
		}
		rollbackErr = errors.Join(rollbackErr, os.RemoveAll(transaction))
		return rollbackErr
	}

	if err := moveDirectoryChildren(target, oldDirectory, filepath.Base(transaction)); err != nil {
		return nil, errors.Join(fmt.Errorf("保留现有证书目录失败：%w", err), rollback())
	}

	if source != "" {
		newDirectory := filepath.Join(transaction, "new")
		if err := copyDirectoryExclusive(source, newDirectory); err != nil {
			return nil, errors.Join(fmt.Errorf("恢复证书目录失败：%w", err), rollback())
		}
		if err := moveDirectoryChildren(newDirectory, target); err != nil {
			return nil, errors.Join(fmt.Errorf("恢复证书目录失败：%w", err), rollback())
		}
	}

	if err := os.RemoveAll(transaction); err != nil {
		return nil, errors.Join(fmt.Errorf("恢复证书目录失败：%w", err), rollback())
	}
	return func() error { return nil }, nil
}

func moveDirectoryChildren(source, target string, excluded ...string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	excludedNames := make(map[string]struct{}, len(excluded))
	for _, name := range excluded {
		excludedNames[name] = struct{}{}
	}
	for _, entry := range entries {
		if _, skip := excludedNames[entry.Name()]; skip {
			continue
		}
		if err := os.Rename(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func clearDirectoryChildren(directory, excluded string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var clearErr error
	for _, entry := range entries {
		if entry.Name() == excluded {
			continue
		}
		clearErr = errors.Join(clearErr, os.RemoveAll(filepath.Join(directory, entry.Name())))
	}
	return clearErr
}

func copyDirectoryExclusive(source, target string) error {
	if pathsOverlap(source, target) {
		return errors.New("复制源目录和目标目录不能互相包含")
	}
	rootInfo, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("检查源目录失败：%w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return errors.New("复制源路径必须是普通目录")
	}
	if _, err := os.Lstat(target); err == nil {
		return errors.New("复制目标目录已存在")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查复制目标目录失败：%w", err)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("目录中不允许包含符号链接：%s", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			mode := info.Mode().Perm()
			if mode == 0 {
				mode = 0o750
			}
			return os.Mkdir(destination, mode)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("目录中包含不支持的文件类型：%s", path)
		}
		return copyFileExclusive(path, destination, info.Mode().Perm())
	})
}

func pathsOverlap(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(filepath.Clean(left))
	rightAbs, rightErr := filepath.Abs(filepath.Clean(right))
	if leftErr != nil || rightErr != nil {
		return true
	}
	return pathContains(leftAbs, rightAbs) || pathContains(rightAbs, leftAbs)
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func writeBackupManifest(directory string) error {
	manifest, err := buildBackupManifest(directory)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("生成备份清单失败：%w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(directory, "manifest.json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建备份清单失败：%w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("写入备份清单失败：%w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("同步备份清单失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭备份清单失败：%w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func buildBackupManifest(directory string) (backupManifest, error) {
	manifest := backupManifest{Version: backupManifestVersion, CreatedAt: time.Now().UTC()}
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == directory || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("备份中不允许包含符号链接：%s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("备份中包含不支持的文件类型：%s", path)
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		digest, err := fileSHA256(path)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, backupManifestFile{
			Path: filepath.ToSlash(relative), Mode: uint32(info.Mode().Perm()), Size: info.Size(), SHA256: digest,
		})
		return nil
	})
	if err != nil {
		return backupManifest{}, fmt.Errorf("生成备份文件清单失败：%w", err)
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	return manifest, nil
}

func validateBackupManifest(directory string) error {
	path := filepath.Join(directory, "manifest.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("备份目录缺少 manifest.json，拒绝恢复未校验的备份")
	}
	if err != nil {
		return fmt.Errorf("检查备份清单失败：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("备份清单必须是普通文件")
	}
	if info.Size() > 8<<20 {
		return errors.New("备份清单超过 8 MiB 安全上限")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取备份清单失败：%w", err)
	}
	var expected backupManifest
	if err := json.Unmarshal(data, &expected); err != nil {
		return fmt.Errorf("解析备份清单失败：%w", err)
	}
	if expected.Version != backupManifestVersion {
		return fmt.Errorf("不支持的备份清单版本：%d", expected.Version)
	}
	if expected.CreatedAt.IsZero() {
		return errors.New("备份清单缺少创建时间")
	}
	if len(expected.Files) == 0 {
		return errors.New("备份清单不包含任何文件")
	}
	actual, err := buildBackupManifestExcludingManifest(directory)
	if err != nil {
		return err
	}
	if len(actual.Files) != len(expected.Files) {
		return errors.New("备份文件数量与清单不一致")
	}
	for index := range expected.Files {
		left, right := expected.Files[index], actual.Files[index]
		if left.Path != right.Path || left.Mode != right.Mode || left.Size != right.Size || !strings.EqualFold(left.SHA256, right.SHA256) {
			return fmt.Errorf("备份文件校验失败：%s", left.Path)
		}
	}
	return nil
}

func buildBackupManifestExcludingManifest(directory string) (backupManifest, error) {
	manifest, err := buildBackupManifest(directory)
	if err != nil {
		return backupManifest{}, err
	}
	filtered := manifest.Files[:0]
	for _, item := range manifest.Files {
		if item.Path != "manifest.json" {
			filtered = append(filtered, item)
		}
	}
	manifest.Files = filtered
	return manifest, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
