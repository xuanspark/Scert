package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type configTempFile interface {
	io.Writer
	Name() string
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}

type configSaveOperations struct {
	createTemp func(string, string) (configTempFile, error)
	ownership  func(string, string) error
	replace    func(string, string) error
}

func defaultConfigSaveOperations() configSaveOperations {
	return configSaveOperations{
		createTemp: func(directory, pattern string) (configTempFile, error) {
			return os.CreateTemp(directory, pattern)
		},
		ownership: applyLinuxServiceOwnership,
		replace:   os.Rename,
	}
}

// saveConfigFile 以同目录文件替换作为提交点，提交之后不再执行可能报错的操作。
// 目录权限和归属调整仍可能留下元数据变化；此处不保证断电后的目录项持久性。
func saveConfigFile(path string, data []byte, operations configSaveOperations) error {
	directory := filepath.Dir(path)
	temporary, err := operations.createTemp(directory, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时配置文件失败：%w", err)
	}
	closed, committed := false, false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		if !committed {
			_ = os.Remove(temporary.Name())
		}
	}()
	if written, err := temporary.Write(data); err != nil {
		return fmt.Errorf("写入临时配置文件失败：%w", err)
	} else if written != len(data) {
		return fmt.Errorf("写入临时配置文件失败：%w", io.ErrShortWrite)
	}
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("设置配置文件权限失败：%w", err)
	}
	if err := operations.ownership(directory, temporary.Name()); err != nil {
		return fmt.Errorf("设置配置文件归属失败：%w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("同步临时配置文件失败：%w", err)
	}
	err = temporary.Close()
	closed = true
	if err != nil {
		return fmt.Errorf("关闭临时配置文件失败：%w", err)
	}
	if err := operations.replace(temporary.Name(), path); err != nil {
		return fmt.Errorf("替换配置文件失败：%w", err)
	}
	committed = true
	return nil
}
