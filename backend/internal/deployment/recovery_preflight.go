package deployment

import (
	"context"
	"errors"
	"fmt"
)

// validateTransactionLocks 在任何文件变更或解锁之前核验完整锁集合。
// 已完成核验的收尾阶段允许锁已不存在，但存在的无归属锁绝不自动接管。
func validateTransactionLocks(ctx context.Context, client Client, record TransactionRecord, allowMissing bool) error {
	if record.Evidence == nil || !validDigest(record.Evidence.LockToken) {
		return errors.New("缺少可信部署锁证据")
	}
	for _, path := range record.LockPaths {
		exists, err := client.Exists(ctx, path)
		if err != nil {
			return fmt.Errorf("检查部署锁失败：%w", err)
		}
		if !exists && allowMissing {
			continue
		}
		if err := checkOwnedLock(ctx, client, path, record.Evidence.LockToken, false); err != nil {
			return fmt.Errorf("部署锁归属预检失败 %s：%w", path, err)
		}
	}
	return nil
}

// validateRollbackFiles 先核验所有旧文件和目标文件，再允许逐个还原，避免部分覆盖现场。
func validateRollbackFiles(ctx context.Context, client Client, record TransactionRecord) error {
	if err := validateTransactionLocks(ctx, client, record, false); err != nil {
		return err
	}
	for _, backup := range record.Backups {
		backupExists, err := validateRecoveryBackup(ctx, client, backup)
		if err != nil {
			return err
		}
		targetExists, err := client.Exists(ctx, backup.Original)
		if err != nil {
			return fmt.Errorf("检查待恢复目标失败：%w", err)
		}
		if backup.OriginalExists && !backupExists {
			if !targetExists {
				return fmt.Errorf("备份文件和目标文件均不存在：%s", backup.Original)
			}
			if err := checkFileDigest(ctx, client, backup.Original, backup.OriginalDigest); err != nil {
				return fmt.Errorf("无法证明旧文件已恢复：%w", err)
			}
			continue
		}
		if !targetExists {
			continue
		}
		if err := checkFileDigest(ctx, client, backup.Original, record.Evidence.Files[backup.Original]); err != nil {
			if !backup.OriginalExists {
				return fmt.Errorf("新增目标文件身份不一致：%w", err)
			}
			if oldErr := checkFileDigest(ctx, client, backup.Original, backup.OriginalDigest); oldErr != nil {
				return fmt.Errorf("目标既不是本次部署文件也不是原始文件：%w", oldErr)
			}
		}
	}
	return nil
}

func validateRecoveryBackup(ctx context.Context, client Client, backup TransactionBackup) (bool, error) {
	exists, err := client.Exists(ctx, backup.Saved)
	if err != nil {
		return false, fmt.Errorf("检查恢复备份失败：%w", err)
	}
	if !exists {
		return false, nil
	}
	if !backup.OriginalExists {
		return true, fmt.Errorf("发现不属于本次事务的备份文件：%s", backup.Saved)
	}
	if err := checkFileDigest(ctx, client, backup.Saved, backup.OriginalDigest); err != nil {
		return true, fmt.Errorf("恢复备份身份不一致：%w", err)
	}
	return true, nil
}
