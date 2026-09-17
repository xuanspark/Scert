//go:build linux

package nginxactivation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func readRegular(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := validateStateFile(info, limit); err != nil {
		return nil, err
	}
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := validateStateFile(opened, limit); err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, errors.New("激活状态文件在读取期间发生变化")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.Join(errors.New("读取激活状态失败"), err)
	}
	return data, nil
}

func validateStateFile(info os.FileInfo, limit int64) error {
	if !info.Mode().IsRegular() || info.Size() > limit {
		return errors.New("激活状态必须是大小受限的普通文件")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("激活状态文件所有者、链接数或写权限不可信")
	}
	return nil
}

func writeExclusive(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	return errors.Join(writeErr, syncErr, file.Close())
}

func writeAtomic(root *os.Root, name string, data []byte) error {
	if info, err := root.Lstat(name); err == nil {
		if err := validateStateFile(info, 64<<10); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	id, err := newID()
	if err != nil {
		return err
	}
	// 失败的临时文件保留供诊断，不覆盖已有快照。
	temporary := ".pending-" + id
	if err := writeExclusive(root, temporary, data); err != nil {
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func newID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func validateDirectory(directory string) error {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == string(filepath.Separator) {
		return errors.New("Nginx 激活目录必须是独立的绝对目录")
	}
	// 目录来自本地部署策略，仍拒绝可改变 Nginx 指令语义的字符。
	for _, char := range directory {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '/' || char == '-' || char == '_' || char == '.') {
			return errors.New("Nginx 激活目录包含不允许的字符")
		}
	}
	for path := directory; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("激活目录及其父路径必须是真实目录：%w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("激活目录及其父路径必须是真实目录")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
			return errors.New("激活目录及其父路径的所有者不可信")
		}
		if path == directory && (info.Mode().Perm() != 0o700 || stat.Uid != uint32(os.Geteuid())) {
			return errors.New("激活目录只能由激活器所属用户访问")
		}
		// 仅允许 root 所有且带粘滞位的共享父目录；其他用户不能替换受信子目录。
		if info.Mode().Perm()&0o022 != 0 && !(stat.Uid == 0 && info.Mode()&os.ModeSticky != 0) {
			return errors.New("激活目录的父路径允许非受信用户写入")
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	return nil
}
