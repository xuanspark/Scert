package certstorage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var safePath = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// Service 的策略仅来自 root 所有的 systemd unit，不读取应用可改写的配置。
type Service struct {
	UID   uint32
	GID   uint32
	Roots []string
	Log   func(error)
}

func within(parent, child string) bool {
	return parent == child || strings.HasPrefix(child, parent+"/")
}

func validatePath(path string, roots []string) error {
	if !safePath.MatchString(path) || len(path) > 1024 || filepath.Clean(path) != path || path == "/" {
		return errors.New("证书目录必须是规范绝对路径")
	}
	for _, protected := range []string{"/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib64", "/proc", "/root", "/run", "/sbin", "/scert", "/sys", "/tmp", "/usr"} {
		if within(protected, path) || within(path, protected) {
			return errors.New("证书目录不能覆盖系统或应用文件")
		}
	}
	if within("/var", path) && !within("/var/lib/scert", path) {
		return errors.New("证书目录不能覆盖其他服务的数据")
	}
	for _, root := range roots {
		if root != "/" && filepath.Clean(root) == root && within(root, path) {
			return nil
		}
	}
	return errors.New("证书目录不在安装时批准的范围内")
}

// prepareDirectory 逐层使用不跟随链接的目录描述符。特权操作要求父目录仅 root 可写，
// 防止运行账户在校验后替换路径；已存在的目录不被夺取所有权或递归修改权限。
func prepareDirectory(path string, uid, gid uint32) error {
	if uid == 0 || gid == 0 {
		return errors.New("目录所有者不能是 root")
	}
	fd, err := unix.Open("/", unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index, part := range parts {
		var parent unix.Stat_t
		if err := unix.Fstat(fd, &parent); err != nil {
			return err
		}
		if parent.Uid != 0 || parent.Mode&0o022 != 0 {
			return errors.New("特权创建要求父目录仅由 root 写入")
		}
		created := false
		next, err := unix.Openat(fd, part, unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) {
			if err = unix.Mkdirat(fd, part, 0o750); err != nil {
				return err
			}
			created = true
			next, err = unix.Openat(fd, part, unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if err != nil {
			return err
		}
		_ = unix.Close(fd)
		fd = next
		last := index == len(parts)-1
		if created {
			if last {
				if err := unix.Fchown(fd, int(uid), int(gid)); err != nil {
					return err
				}
				if err := unix.Fchmod(fd, 0o700); err != nil {
					return err
				}
			} else {
				if err := unix.Fchmod(fd, 0o755); err != nil {
					return err
				}
			}
			if err := unix.Fsync(fd); err != nil {
				return err
			}
		}
		if last {
			var info unix.Stat_t
			if err := unix.Fstat(fd, &info); err != nil {
				return err
			}
			if info.Uid != uid || info.Gid != gid || info.Mode&0o027 != 0 {
				return errors.New("现有目录不属于 Scert 或权限过宽，拒绝接管")
			}
		}
	}
	return nil
}

func systemctl(ctx context.Context, args ...string) error {
	// 参数为固定动作、固定服务及校验后的路径，不经过 shell。
	output, err := exec.CommandContext(ctx, "/usr/bin/systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("目录服务调用 systemd 失败：%w：%s", err, output)
	}
	return nil
}

func (s Service) prepare(ctx context.Context, path string) error {
	if err := validatePath(path, s.Roots); err != nil {
		return err
	}
	if err := prepareDirectory(path, s.UID, s.GID); err != nil {
		return err
	}
	// 不重启正在初始化的进程，保留数据库、DNS 和浏览器的一次性验证会话。
	if err := systemctl(ctx, "bind", "--mkdir", "Scert.service", path, path); err != nil {
		return err
	}
	return persistPermission(ctx, path)
}

func persistPermission(ctx context.Context, path string) error {
	const directory = "/etc/systemd/system/Scert.service.d"
	// 写入位置的全部父目录必须受 root 保护，不能被应用替换成链接。
	for current := directory; ; current = filepath.Dir(current) {
		var stat unix.Stat_t
		if err := unix.Lstat(current, &stat); err != nil {
			return err
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != 0 || stat.Mode&0o022 != 0 {
			return errors.New("服务权限配置目录不可信")
		}
		if current == "/" {
			break
		}
	}
	name := fmt.Sprintf("60-storage-%x.conf", sha256.Sum256([]byte(path)))
	content := []byte("[Service]\nReadWritePaths=" + path + "\n")
	target := filepath.Join(directory, name)
	if existing, err := os.ReadFile(target); err == nil {
		if !bytes.Equal(existing, content) {
			return errors.New("已有目录授权配置不匹配，拒绝覆盖")
		}
		return systemctl(ctx, "daemon-reload")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return systemctl(ctx, "daemon-reload")
}

func (s Service) Serve(ctx context.Context, listener *net.UnixListener) error {
	if os.Geteuid() != 0 || s.UID == 0 || s.GID == 0 {
		return errors.New("目录服务身份配置无效")
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	// 顺序处理，避免同时修改挂载点和持久授权。
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.handle(ctx, connection)
	}
}

func (s Service) handle(ctx context.Context, connection *net.UnixConn) {
	defer connection.Close()
	uid, err := peerUID(connection)
	if err != nil || uid != s.UID {
		return
	}
	_ = connection.SetDeadline(time.Now().Add(25 * time.Second))
	line, err := bufio.NewReaderSize(connection, 2048).ReadSlice('\n')
	if err != nil {
		return
	}
	var message request
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&message) != nil {
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return
	}
	operation, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err = s.prepare(operation, message.Path)
	if err != nil && s.Log != nil {
		s.Log(err)
	}
	_ = json.NewEncoder(connection).Encode(response{Ready: err == nil})
}
