package nginxactivation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Initialize 仅准备首次接线材料，不修改站点、不重载 Nginx，也不覆盖已有状态。
func (r Rotation) Initialize(ctx context.Context, certificatePath, privateKeyPath string) (string, error) {
	root, lock, err := r.open(ctx)
	if err != nil {
		return "", err
	}
	defer root.Close()
	defer lock.Close()
	if err := requireUninitialized(root); err != nil {
		return "", err
	}
	chain, err := readInitializationMaterial(certificatePath, 1<<20, false)
	if err != nil {
		return "", err
	}
	key, err := readInitializationMaterial(privateKeyPath, 64<<10, true)
	if err != nil {
		return "", err
	}
	defer clear(key)
	leaf, err := r.validateMaterial(ctx, chain, key)
	if err != nil {
		return "", err
	}
	if err := r.Proxy.Wait(ctx, leaf.Raw); err != nil {
		return "", err
	}
	if err := r.Upstream.Wait(ctx, leaf.Raw); err != nil {
		return "", err
	}
	if err := r.Reloader.Check(ctx); err != nil {
		return "", fmt.Errorf("初始化前 Nginx 配置检查失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := requireUninitialized(root); err != nil {
		return "", err
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	if err := root.Mkdir(id, 0o700); err != nil {
		return "", err
	}
	if err := writeExclusive(root, filepath.Join(id, "fullchain.pem"), chain); err != nil {
		return "", err
	}
	if err := writeExclusive(root, filepath.Join(id, "key.pem"), key); err != nil {
		return "", err
	}
	version, err := root.Open(id)
	if err != nil {
		return "", err
	}
	if err := errors.Join(version.Sync(), version.Close()); err != nil {
		return "", err
	}
	// 最后独占创建引用；断电或写入失败后的残留必须人工核对，不能自动覆盖重试。
	if err := writeExclusive(root, includeName, renderInclude(r.Directory, id)); err != nil {
		return "", err
	}
	directory, err := root.Open(".")
	if err != nil {
		return "", err
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return "", err
	}
	return id, nil
}

func requireUninitialized(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != ".activation.lock" {
			return errors.New("激活目录存在旧状态或初始化残留，拒绝覆盖，请先人工核对")
		}
	}
	return nil
}

func readInitializationMaterial(path string, limit int64, private bool) ([]byte, error) {
	if err := validateOwnedFile(path, uint32(os.Geteuid()), false); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if private {
		info, err := root.Lstat(filepath.Base(path))
		if err != nil {
			return nil, err
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("初始化私钥不能允许组或其他用户读取")
		}
	}
	return readRegular(root, filepath.Base(path), limit)
}
