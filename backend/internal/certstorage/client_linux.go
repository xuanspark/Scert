package certstorage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const SocketPath = "/run/scert-storage/prepare.sock"

type request struct {
	Path string `json:"path"`
}

type response struct {
	Ready bool `json:"ready"`
}

func peerUID(connection *net.UnixConn) (uint32, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credentials *unix.Ucred
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		credentials, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil || socketErr != nil {
		return 0, errors.Join(err, socketErr)
	}
	if credentials == nil || credentials.Pid <= 0 {
		return 0, errors.New("本地目录服务缺少内核身份")
	}
	return credentials.Uid, nil
}

// Prepare 只使用受保护的本机通道；不接受远程地址，也不传递命令或服务名称。
func Prepare(ctx context.Context, path string) error {
	for current := SocketPath; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("本地目录服务路径不可信")
		}
		if current == SocketPath {
			if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0o007 != 0 {
				return errors.New("本地目录服务通道权限无效")
			}
		} else if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
			return errors.New("本地目录服务父目录权限无效")
		}
		if current == "/" {
			break
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", SocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	uid, err := peerUID(connection.(*net.UnixConn))
	if err != nil || uid != 0 {
		return errors.New("本地目录服务不是受信任的系统进程")
	}
	deadline, _ := ctx.Deadline()
	_ = connection.SetDeadline(deadline)
	if err := json.NewEncoder(connection).Encode(request{Path: path}); err != nil {
		return err
	}
	var result response
	decoder := json.NewDecoder(io.LimitReader(connection, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return err
	}
	if !result.Ready {
		return errors.New("目录自动准备失败，请检查获准范围、父目录权限及目录服务状态")
	}
	return nil
}
