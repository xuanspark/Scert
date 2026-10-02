package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SecretReader interface {
	Get(ctx context.Context, reference string) (map[string]string, error)
}

type SSHFactory struct {
	Secrets           SecretReader
	Timeout           time.Duration
	HostKeyAlgorithms []string
}

func (f SSHFactory) Connect(ctx context.Context, server Server) (Client, error) {
	return f.connect(ctx, server, true)
}

func (f SSHFactory) ConnectShell(ctx context.Context, server Server) (Client, error) {
	return f.connect(ctx, server, false)
}

func (f SSHFactory) connect(ctx context.Context, server Server, requireSFTP bool) (Client, error) {
	server = server.Normalize()
	if err := server.Validate(); err != nil {
		return nil, err
	}
	if server.AuthMethod != SSHAuthPassword {
		return nil, errors.New("服务器连接仅支持 SSH 密码认证")
	}
	if f.Secrets == nil {
		return nil, errors.New("SSH 敏感信息存储未初始化")
	}
	secrets, err := f.Secrets.Get(ctx, server.PasswordRef)
	if err != nil {
		return nil, fmt.Errorf("读取 SSH 认证信息失败：%w", err)
	}
	password := secrets["password"]
	if password == "" {
		return nil, errors.New("SSH 密码不能为空")
	}
	authMethods := []ssh.AuthMethod{ssh.Password(password)}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	algorithms, err := f.secureHostKeyAlgorithms()
	if err != nil {
		return nil, err
	}
	timeoutContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sshClient, err := connectSSHClient(timeoutContext, server, authMethods, algorithms, len(f.HostKeyAlgorithms) == 0)
	if err != nil {
		return nil, err
	}
	adapter := &sshClientAdapter{ssh: sshClient}
	if !requireSFTP {
		return adapter, nil
	}
	sftpClient, err := sshOperation(timeoutContext, adapter, func() (*sftp.Client, error) {
		return sftp.NewClient(sshClient)
	})
	if err != nil {
		_ = sshClient.Close()
		return nil, fmt.Errorf("创建 SFTP 客户端失败：%w", err)
	}
	adapter.sftp = sftpClient
	return adapter, nil
}

func (f SSHFactory) secureHostKeyAlgorithms() ([]string, error) {
	supported := ssh.SupportedAlgorithms().HostKeys
	if len(f.HostKeyAlgorithms) == 0 {
		algorithms := make([]string, 0, len(supported))
		for _, algorithm := range supported {
			// 默认只尝试普通主机密钥算法，避免为不可能存在的证书算法重复建立 SSH 连接。
			if strings.HasSuffix(algorithm, "-cert-v01@openssh.com") {
				continue
			}
			algorithms = append(algorithms, algorithm)
		}
		return algorithms, nil
	}
	supportedSet := make(map[string]struct{}, len(supported))
	for _, algorithm := range supported {
		supportedSet[algorithm] = struct{}{}
	}
	algorithms := make([]string, 0, len(f.HostKeyAlgorithms))
	seen := make(map[string]struct{}, len(f.HostKeyAlgorithms))
	for _, algorithm := range f.HostKeyAlgorithms {
		algorithm = strings.TrimSpace(algorithm)
		if _, ok := supportedSet[algorithm]; !ok {
			continue
		}
		if _, ok := seen[algorithm]; ok {
			continue
		}
		seen[algorithm] = struct{}{}
		algorithms = append(algorithms, algorithm)
	}
	if len(algorithms) == 0 {
		return nil, errors.New("SSH 主机密钥算法列表不包含受支持的安全算法")
	}
	return algorithms, nil
}

func connectSSHClient(
	ctx context.Context,
	server Server,
	authMethods []ssh.AuthMethod,
	algorithms []string,
	preferSingleNegotiation bool,
) (*ssh.Client, error) {
	address := net.JoinHostPort(server.Address, fmt.Sprintf("%d", server.Port))
	var mismatchErrors []string
	algorithmGroups := make([][]string, 0, len(algorithms)+1)
	if preferSingleNegotiation {
		algorithmGroups = append(algorithmGroups, algorithms)
	}
	for _, algorithm := range algorithms {
		algorithmGroups = append(algorithmGroups, []string{algorithm})
	}
	for _, algorithmGroup := range algorithmGroups {
		algorithm := strings.Join(algorithmGroup, ",")
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("连接 SSH 服务器超时：%w", err)
		}
		connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, fmt.Errorf("连接 SSH 服务器失败：%w", err)
		}
		if deadline, ok := ctx.Deadline(); ok {
			if err := connection.SetDeadline(deadline); err != nil {
				_ = connection.Close()
				return nil, fmt.Errorf("设置 SSH 连接超时失败：%w", err)
			}
		}
		hostKeyMatched := false
		hostKeyMismatch := ""
		config := &ssh.ClientConfig{
			User:              server.Username,
			Auth:              authMethods,
			HostKeyAlgorithms: algorithmGroup,
			HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
				fingerprint := ssh.FingerprintSHA256(key)
				if fingerprint != server.HostKeyFingerprint {
					hostKeyMismatch = fmt.Sprintf("%s 返回 %s", algorithm, fingerprint)
					return fmt.Errorf("SSH 主机指纹不匹配：服务器 %s %s", server.Name, hostKeyMismatch)
				}
				hostKeyMatched = true
				return nil
			},
		}
		stopCancel := context.AfterFunc(ctx, func() { _ = connection.Close() })
		clientConn, channels, requests, handshakeErr := ssh.NewClientConn(connection, address, config)
		stopCancel()
		if err := ctx.Err(); err != nil {
			_ = connection.Close()
			return nil, fmt.Errorf("SSH 握手已取消：%w", err)
		}
		if handshakeErr == nil {
			if err := connection.SetDeadline(time.Time{}); err != nil {
				_ = clientConn.Close()
				return nil, fmt.Errorf("清除 SSH 连接超时失败：%w", err)
			}
			return ssh.NewClient(clientConn, channels, requests), nil
		}
		_ = connection.Close()
		if hostKeyMatched {
			return nil, fmt.Errorf("SSH 握手失败：%w", handshakeErr)
		}
		if hostKeyMismatch != "" {
			mismatchErrors = append(mismatchErrors, hostKeyMismatch)
			continue
		}
		var negotiationError *ssh.AlgorithmNegotiationError
		if len(algorithmGroup) == 1 && errors.As(handshakeErr, &negotiationError) && negotiationError.What == "host key" {
			continue
		}
		return nil, fmt.Errorf("SSH 握手失败：%w", handshakeErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("连接 SSH 服务器超时：%w", err)
	}
	if len(mismatchErrors) == 0 {
		return nil, fmt.Errorf("SSH 主机指纹不匹配：服务器 %s 未提供已登记的主机密钥", server.Name)
	}
	return nil, fmt.Errorf(
		"SSH 主机指纹不匹配：服务器 %s 未提供已登记的主机密钥；已校验 %s",
		server.Name,
		strings.Join(mismatchErrors, "；"),
	)
}

type sshClientAdapter struct {
	ssh          *ssh.Client
	sftp         *sftp.Client
	disconnected atomic.Bool
}

// sshOperation 取消时关闭传输并等待操作退出，避免回滚与仍在写入的操作并行。
// 已断开的连接由部署事务使用独立回滚上下文重新建立，仍严格校验主机指纹。
func sshOperation[T any](ctx context.Context, c *sshClientAdapter, operation func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		value, err := operation()
		done <- outcome{value, err}
	}()
	select {
	case result := <-done:
		if err := ctx.Err(); err != nil {
			c.disconnected.Store(true)
			_ = c.ssh.Close()
			return zero, err
		}
		var networkErr *net.OpError
		if errors.Is(result.err, io.EOF) || errors.Is(result.err, io.ErrUnexpectedEOF) || errors.Is(result.err, net.ErrClosed) || errors.As(result.err, &networkErr) {
			c.disconnected.Store(true)
			_ = c.ssh.Close()
		}
		return result.value, result.err
	case <-ctx.Done():
		c.disconnected.Store(true)
		_ = c.ssh.Close()
		<-done
		return zero, ctx.Err()
	}
}

func (c *sshClientAdapter) Disconnected() bool { return c.disconnected.Load() }

func (c *sshClientAdapter) fileOperation(ctx context.Context, operation func() error) error {
	_, err := sshOperation(ctx, c, func() (struct{}, error) { return struct{}{}, operation() })
	return err
}

func (c *sshClientAdapter) MkdirAll(ctx context.Context, path string) error {
	return c.fileOperation(ctx, func() error { return c.sftp.MkdirAll(path) })
}

func (c *sshClientAdapter) List(ctx context.Context, directory string) ([]string, error) {
	return sshOperation(ctx, c, func() ([]string, error) {
		entries, err := c.sftp.ReadDir(directory)
		if err != nil {
			return nil, err
		}
		paths := make([]string, 0, len(entries))
		for _, entry := range entries {
			paths = append(paths, path.Join(directory, entry.Name()))
		}
		return paths, nil
	})
}

// 远程原子建目录用于协作锁，跨进程共享；已有锁绝不自动抢占或清理。
func (c *sshClientAdapter) Lock(ctx context.Context, path string) error {
	return c.fileOperation(ctx, func() error { return c.sftp.Mkdir(path) })
}

func (c *sshClientAdapter) Unlock(ctx context.Context, path string) error {
	return c.fileOperation(ctx, func() error {
		if err := c.sftp.Remove(path + "/owner"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return c.sftp.RemoveDirectory(path)
	})
}

func (c *sshClientAdapter) CreateOwnedLock(ctx context.Context, path, token string) error {
	if !validDigest(token) {
		return errors.New("部署锁令牌格式无效")
	}
	return c.Upload(ctx, path+"/owner", []byte(token), 0o600)
}

func (c *sshClientAdapter) ReadLockOwner(ctx context.Context, path string) (string, error) {
	return sshOperation(ctx, c, func() (string, error) {
		file, err := c.sftp.Open(path + "/owner")
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		return strings.TrimSpace(string(data)), err
	})
}

func (c *sshClientAdapter) FileDigest(ctx context.Context, path string) (string, error) {
	return sshOperation(ctx, c, func() (string, error) {
		info, err := c.sftp.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Size() > 16*1024*1024 {
			return "", errors.New("证书文件类型或大小无效")
		}
		file, err := c.sftp.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		hash := sha256.New()
		count, err := io.Copy(hash, io.LimitReader(file, 16*1024*1024+1))
		if err != nil {
			return "", err
		}
		if count > 16*1024*1024 {
			return "", errors.New("证书文件超过摘要读取上限")
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	})
}

func (c *sshClientAdapter) Exists(ctx context.Context, path string) (bool, error) {
	return sshOperation(ctx, c, func() (bool, error) {
		_, err := c.sftp.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return err == nil, err
	})
}

func (c *sshClientAdapter) Upload(ctx context.Context, path string, data []byte, mode uint32) error {
	var created atomic.Bool
	err := c.fileOperation(ctx, func() error {
		// 只创建新临时文件，不跟随已有符号链接，也不截断已存在的文件。
		file, err := c.sftp.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return err
		}
		created.Store(true)
		// 先收紧空文件权限，再写入任何证书或私钥内容。
		if err := file.Chmod(os.FileMode(mode)); err != nil {
			_ = file.Close()
			return err
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	})
	if err != nil && !created.Load() {
		// 未确认取得所有权的路径不得交给事务删除，包括创建响应丢失的情况。
		return &uploadNotCreatedError{err: err}
	}
	return err
}

type uploadNotCreatedError struct{ err error }

func (e *uploadNotCreatedError) Error() string { return e.err.Error() }
func (e *uploadNotCreatedError) Unwrap() error { return e.err }

func (c *sshClientAdapter) Rename(ctx context.Context, source, target string) error {
	return c.fileOperation(ctx, func() error { return c.sftp.Rename(source, target) })
}

func (c *sshClientAdapter) Remove(ctx context.Context, path string) error {
	return c.fileOperation(ctx, func() error {
		err := c.sftp.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

func (c *sshClientAdapter) Run(ctx context.Context, command []string) (CommandResult, error) {
	if len(command) == 0 {
		return CommandResult{}, errors.New("远程命令不能为空")
	}
	if err := validateCommand(command); err != nil {
		return CommandResult{}, err
	}
	return sshOperation(ctx, c, func() (CommandResult, error) { return c.runCommand(command) })
}

func (c *sshClientAdapter) runCommand(command []string) (CommandResult, error) {
	session, err := c.ssh.NewSession()
	if err != nil {
		return CommandResult{}, err
	}
	defer session.Close()
	outputBuffer := &commandOutput{}
	session.Stdout = outputBuffer
	session.Stderr = outputBuffer
	err = session.Run(joinShellArgs(command))
	output := outputBuffer.Bytes()
	if err != nil {
		var exitError *ssh.ExitError
		if errors.As(err, &exitError) {
			return CommandResult{Output: output, ExitCode: exitError.ExitStatus()}, nil
		}
		return CommandResult{Output: output, ExitCode: -1}, err
	}
	return CommandResult{Output: output}, nil
}

func (c *sshClientAdapter) Close() error {
	var first error
	// 先关闭传输，避免无响应的 SFTP 服务阻塞关闭过程。
	if c.ssh != nil {
		c.disconnected.Store(true)
		first = c.ssh.Close()
	}
	if c.sftp != nil {
		if err := c.sftp.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func validateCommand(command []string) error {
	for index, token := range command {
		if strings.TrimSpace(token) == "" || strings.ContainsRune(token, '\x00') || (strings.ContainsAny(token, "\r\n") && !(len(command) == 3 && index == 2 && command[0] == "sh" && command[1] == "-c")) {
			return errors.New("远程命令参数无效")
		}
	}
	return nil
}

func joinShellArgs(command []string) string {
	quoted := make([]string, len(command))
	for index, token := range command {
		quoted[index] = "'" + strings.ReplaceAll(token, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

var _ io.Closer = (*sshClientAdapter)(nil)
