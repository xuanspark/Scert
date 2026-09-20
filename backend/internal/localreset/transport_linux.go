//go:build linux

package localreset

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const DefaultPath = "/run/scert/admin-control.sock"

func socketPath(path string) (string, error) {
	if path == "" {
		path = DefaultPath
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 107 {
		return "", ErrUnavailable
	}
	return path, nil
}

// validateDirectory 拒绝符号链接及不可信的可写上级目录。
func validateDirectory(parent string, owner uint32) error {
	for path := parent; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrUnauthorized
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != owner && stat.Uid != 0) {
			return ErrUnauthorized
		}
		if path == parent {
			if stat.Uid != owner || info.Mode().Perm()&0022 != 0 {
				return ErrUnauthorized
			}
		} else if info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return ErrUnauthorized
		}
		if path == filepath.Dir(path) {
			return nil
		}
	}
}

func validateSocket(path string, owner uint32) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0077 != 0 {
		return nil, ErrUnauthorized
	}
	return info, nil
}

func Listen(path string) (*Server, error) {
	path, err := socketPath(path)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(path)
	if err := os.Mkdir(parent, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	owner := uint32(os.Geteuid())
	if err := validateDirectory(parent, owner); err != nil {
		return nil, err
	}
	info, err := validateSocket(path, owner)
	if err == nil {
		conn, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return nil, ErrUnavailable
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, ErrUnavailable
		}
		current, checkErr := validateSocket(path, owner)
		if checkErr != nil || !os.SameFile(info, current) {
			return nil, ErrUnauthorized
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	info, err = os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	cleanup := func() error {
		current, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !os.SameFile(info, current) {
			return ErrUnauthorized
		}
		return os.Remove(path)
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		_ = cleanup()
		return nil, err
	}
	authorize := func(conn net.Conn) (Peer, error) {
		uid, err := peerUID(conn)
		if err != nil || (uid != 0 && uid != owner) {
			return Peer{}, ErrUnauthorized
		}
		return Peer{Identity: "uid:" + strconv.FormatUint(uint64(uid), 10)}, nil
	}
	return newServer(listener, authorize, cleanup), nil
}

func peerUID(conn net.Conn) (uint32, error) {
	socket, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, ErrUnauthorized
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credential *unix.Ucred
	var credentialErr error
	err = raw.Control(func(fd uintptr) {
		credential, credentialErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil || credentialErr != nil || credential == nil || credential.Pid <= 0 {
		return 0, ErrUnauthorized
	}
	return credential.Uid, nil
}

// Dial 同时验证目录、套接字和服务进程身份，不能只信任路径名称。
func Dial(ctx context.Context, path string, expectedUID uint32) (net.Conn, error) {
	path, err := socketPath(path)
	if err != nil {
		return nil, err
	}
	if err := validateDirectory(filepath.Dir(path), expectedUID); err != nil {
		return nil, err
	}
	before, err := validateSocket(path, expectedUID)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	uid, err := peerUID(conn)
	after, checkErr := validateSocket(path, expectedUID)
	if err != nil || uid != expectedUID || checkErr != nil || !os.SameFile(before, after) {
		_ = conn.Close()
		return nil, fmt.Errorf("%w", ErrUnauthorized)
	}
	return conn, nil
}
