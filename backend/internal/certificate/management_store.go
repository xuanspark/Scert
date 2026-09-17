package certificate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 不含点的固定目录名不能与经过校验的业务域名目录重合。
const managementDirectory = "management-domain"

func managementFileStore(certificateRoot, backupRoot string) (FileStore, error) {
	certificateRoot = strings.TrimSpace(certificateRoot)
	backupRoot = strings.TrimSpace(backupRoot)
	if !filepath.IsAbs(certificateRoot) || !filepath.IsAbs(backupRoot) {
		return FileStore{}, errors.New("管理证书和备份根目录必须是绝对路径")
	}
	return FileStore{
		Root:           filepath.Join(certificateRoot, managementDirectory),
		BackupRoot:     filepath.Join(backupRoot, managementDirectory),
		RetainPrevious: true,
	}, nil
}

// 同时检查配置路径与符号链接实际指向；无法确认时保留旧版本并返回错误。
func (s FileStore) isProtectedDirectory(directory string) (bool, error) {
	if len(s.ProtectedPaths) == 0 {
		return false, nil
	}
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return false, err
	}
	resolvedDirectory, err := filepath.EvalSymlinks(absoluteDirectory)
	if err != nil {
		return false, err
	}
	for _, path := range s.ProtectedPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return false, err
		}
		if pathWithinDirectory(absoluteDirectory, absolutePath) {
			return true, nil
		}
		resolvedPath, err := filepath.EvalSymlinks(absolutePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("解析管理证书引用失败：%w", err)
		}
		if pathWithinDirectory(resolvedDirectory, resolvedPath) {
			return true, nil
		}
	}
	return false, nil
}

func pathWithinDirectory(directory, path string) bool {
	relative, err := filepath.Rel(directory, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
