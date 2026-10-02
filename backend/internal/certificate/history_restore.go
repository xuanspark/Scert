package certificate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// restoreResource 使用已经校验的内存快照，避免预检之后重新读取另一个历史文件。
// 状态提交失败时恢复原目录和元数据；本地恢复不代表远端服务器已更新。
func (s FileStore) restoreResource(ctx context.Context, domain, certificateType string, resource Resource, expected []string, commit func(SavedResource) error) (SavedResource, error) {
	if err := ctx.Err(); err != nil {
		return SavedResource{}, err
	}
	resource, err := ValidateResourceForDomains(resource, expected)
	if err != nil {
		return SavedResource{}, fmt.Errorf("历史证书不匹配当前域名配置：%w", err)
	}
	previous, err := s.CurrentDirectory(domain, certificateType)
	if err != nil {
		return SavedResource{}, err
	}
	hadPrevious := directoryContainsCertificateFiles(previous)
	// 固定保存前的目录。回滚不能仅将旧元数据写入新目录而丢失原文件引用。
	saved, saveErr := s.SaveForType(ctx, domain, certificateType, resource)
	rollback := func(cause error) (SavedResource, error) {
		if saved.Directory == "" {
			return SavedResource{}, cause
		}
		if hadPrevious {
			if saved.BackupDirectory == "" {
				return SavedResource{}, errors.Join(cause, errors.New("恢复失败且缺少原当前证书归档"))
			}
			if err := restoreCertificateFiles(previous, saved.BackupDirectory); err != nil {
				return SavedResource{}, errors.Join(cause, fmt.Errorf("恢复原当前证书失败：%w", err))
			}
			if err := rewriteRestoredMetadata(previous); err != nil {
				return SavedResource{}, errors.Join(cause, fmt.Errorf("恢复原当前元数据失败：%w", err))
			}
		}
		typeDirectory := filepath.Join(s.Root, directoryName(domain), certificateType)
		if err := removeCertificateDirectory(saved.Directory, typeDirectory); err != nil {
			return SavedResource{}, errors.Join(cause, fmt.Errorf("清理未提交的恢复版本失败：%w", err))
		}
		return SavedResource{}, cause
	}
	if saveErr != nil {
		return rollback(saveErr)
	}
	root, err := openHistoryRoot(s.Root)
	if err != nil {
		return rollback(err)
	}
	defer root.Close()
	absolute, err := filepath.Abs(saved.Directory)
	if err != nil {
		return rollback(err)
	}
	relative, err := filepath.Rel(root.Name(), absolute)
	if err != nil {
		return rollback(err)
	}
	actual, err := readHistoryEntry(root, relative, domain, certificateType)
	if err != nil {
		return rollback(fmt.Errorf("恢复后读取证书失败：%w", err))
	}
	if _, err := ValidateResourceForDomains(actual.resource, expected); err != nil {
		return rollback(fmt.Errorf("恢复后证书校验失败：%w", err))
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	saved.HistoryID = actual.saved.HistoryID
	saved.Current = true
	if commit != nil {
		if err := commit(saved); err != nil {
			return rollback(fmt.Errorf("恢复后更新证书状态失败：%w", err))
		}
	}
	return saved, nil
}

// validateCertificateWritePath 拒绝已有路径上的符号链接，不能借恢复写入其他目录。
func validateCertificateWritePath(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return errors.New("证书写入路径必须为真实目录，不能使用符号链接")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}
