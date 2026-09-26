package bootstrap

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// prepareCertificateRoot 在申请证书前验证路径，并以实际运行身份探测创建、写入和清理权限。
func prepareCertificateRoot(paths config.PathsConfig, requested string) (string, error) {
	root := strings.TrimSpace(requested)
	if root == "" {
		root = paths.Certificate
	}
	root = filepath.Clean(root)
	invalid := func(err error) (string, error) {
		return "", completeError(CompleteErrorCertificatePath, "证书部署根目录无效", err)
	}
	unwritable := func(err error) (string, error) {
		return "", completeError(CompleteErrorCertificateAccess, "证书部署根目录不可写", err)
	}
	if !filepath.IsAbs(root) || filepath.Dir(root) == root || len(root) > 4096 || strings.ContainsAny(root, "\x00\r\n") {
		return invalid(nil)
	}
	// 允许安装目录下独立的证书子目录，但不能覆盖安装目录本身或其他运行数据。
	if pathWithinCertificateRoot(root, paths.Install) {
		return invalid(nil)
	}
	for _, protected := range []string{paths.Config, paths.Data, paths.Log, paths.Backup} {
		if pathWithinCertificateRoot(root, protected) || pathWithinCertificateRoot(protected, root) {
			return invalid(nil)
		}
	}
	for current := root; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return invalid(nil)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return unwritable(err)
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return unwritable(err)
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return unwritable(err)
	}
	defer directory.Close()
	// 探测文件使用随机名称和排他创建，只清理本次创建的文件，不修改已有文件。
	name := ".scert-write-check-" + rand.Text()
	probe, err := directory.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return unwritable(err)
	}
	_, writeErr := probe.Write([]byte("scert"))
	syncErr := probe.Sync()
	closeErr := probe.Close()
	removeErr := directory.Remove(name)
	if err := errors.Join(writeErr, syncErr, closeErr, removeErr); err != nil {
		return unwritable(err)
	}
	return root, nil
}

func pathWithinCertificateRoot(parent, child string) bool {
	if strings.TrimSpace(parent) == "" || strings.TrimSpace(child) == "" {
		return false
	}
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
