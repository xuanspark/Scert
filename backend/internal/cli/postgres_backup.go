package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func (tools *postgresTools) backup(ctx context.Context, connection config.DatabaseConfig, output string) (result error) {
	dsn, env, err := postgresToolConnection(connection)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("创建 PostgreSQL 备份文件失败，目标不得已存在")
	}
	defer func() {
		_ = file.Close()
		if result != nil {
			// 只清理本次独占创建的不完整文件，不触碰既有备份。
			_ = os.Remove(output)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	args := []string{"--format=custom", "--no-password", "--lock-wait-timeout=30s", "--dbname=" + dsn}
	if err := tools.run(ctx, tools.dumpPath, args, env, file); err != nil {
		return errors.Join(errors.New("创建 PostgreSQL 一致性快照失败"), err)
	}
	if err := file.Sync(); err != nil {
		return errors.New("同步 PostgreSQL 备份文件失败")
	}
	if err := file.Close(); err != nil {
		return errors.New("关闭 PostgreSQL 备份文件失败")
	}
	return tools.validateArchive(ctx, output)
}

func (tools *postgresTools) validateArchive(ctx context.Context, input string) error {
	if !isRegularFile(input) {
		return errors.New("PostgreSQL 备份目录缺少有效的 scert.pgdump 文件")
	}
	file, err := os.Open(input)
	if err != nil {
		return errors.New("无法读取 PostgreSQL 备份")
	}
	var magic [5]byte
	_, readErr := io.ReadFull(file, magic[:])
	_ = file.Close()
	if readErr != nil || string(magic[:]) != "PGDMP" {
		return errors.New("PostgreSQL 备份必须是 pg_dump 自定义格式归档")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	// 完整解码归档而非仅检查目录；不连接数据库，不保留含业务数据的 SQL 输出。
	if err := tools.run(ctx, tools.restorePath, []string{"--file=-", "--", input}, postgresToolEnvironment(), io.Discard); err != nil {
		return errors.Join(errors.New("PostgreSQL 归档完整性校验失败"), err)
	}
	return nil
}

var postgresArchiveEntry = regexp.MustCompile(`^\d+;\s+\d+\s+\d+\s+(.+)$`)

func (tools *postgresTools) inventory(ctx context.Context, input string) (map[string]struct{}, error) {
	output := &limitedPostgresOutput{remaining: 8 << 20}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := tools.run(ctx, tools.restorePath, []string{"--list", "--", input}, postgresToolEnvironment(), output); err != nil {
		return nil, errors.Join(errors.New("读取 PostgreSQL 归档对象清单失败"), err)
	}
	items := make(map[string]struct{})
	for _, line := range strings.Split(output.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		match := postgresArchiveEntry.FindStringSubmatch(line)
		if len(match) != 2 {
			return nil, errors.New("PostgreSQL 归档对象清单格式无效")
		}
		if _, duplicate := items[match[1]]; duplicate {
			return nil, errors.New("PostgreSQL 归档对象清单存在重复条目")
		}
		items[match[1]] = struct{}{}
	}
	if len(items) == 0 {
		return nil, errors.New("PostgreSQL 归档对象清单为空，拒绝无法验证的恢复")
	}
	return items, nil
}

func (tools *postgresTools) validateRestoreInventory(ctx context.Context, input, snapshot string) error {
	source, err := tools.inventory(ctx, input)
	if err != nil {
		return err
	}
	target, err := tools.inventory(ctx, snapshot)
	if err != nil {
		return err
	}
	// pg_restore --clean 只删除归档列出的对象。严格相等保证导入及反向回滚
	// 都不遗留额外对象；跨迁移版本或空库灾难恢复需管理员使用原生流程。
	if len(source) != len(target) {
		return errors.New("PostgreSQL 备份与当前数据库对象清单不一致，拒绝自动恢复；请在隔离数据库使用原生工具演练迁移或灾难恢复")
	}
	for item := range source {
		if _, exists := target[item]; !exists {
			return errors.New("PostgreSQL 备份与当前数据库对象或所有者不一致，拒绝可能残留对象的恢复")
		}
	}
	return nil
}

func (tools *postgresTools) createRestoreSnapshot(ctx context.Context, cfg config.Config, connection config.DatabaseConfig) (string, error) {
	root := filepath.Clean(strings.TrimSpace(cfg.Paths.Backup))
	if root == "." {
		return "", errors.New("PostgreSQL 恢复前快照目录不能为空")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("创建 PostgreSQL 恢复前快照目录失败：%w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("PostgreSQL 恢复前快照目录必须为普通目录")
	}
	directory, err := os.MkdirTemp(root, "scert-postgres-before-restore-")
	if err != nil {
		return "", errors.New("创建 PostgreSQL 恢复前快照失败")
	}
	output := filepath.Join(directory, "scert.pgdump")
	if err := tools.backup(ctx, connection, output); err != nil {
		return "", err
	}
	if err := syncDirectory(directory); err != nil {
		return "", errors.New("同步 PostgreSQL 恢复前快照目录失败，拒绝执行恢复")
	}
	if err := syncDirectory(root); err != nil {
		return "", errors.New("同步 PostgreSQL 快照父目录失败，拒绝执行恢复")
	}
	// 成功后也保留原数据库快照，便于人工恢复，不自动清理用户备份。
	return output, nil
}

func (tools *postgresTools) restore(ctx context.Context, connection config.DatabaseConfig, input string) error {
	dsn, env, err := postgresToolConnection(connection)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	// 保留对象所有者和授权，权限或角色不足时失败，而不是悄悄丢失访问控制。
	args := []string{"--clean", "--if-exists", "--single-transaction", "--exit-on-error", "--no-password", "--dbname=" + dsn, "--", input}
	if err := tools.run(ctx, tools.restorePath, args, env, io.Discard); err != nil {
		return errors.Join(errors.New("PostgreSQL 单事务恢复失败"), err)
	}
	return nil
}
