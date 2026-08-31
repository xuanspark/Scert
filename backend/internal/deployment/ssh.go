package deployment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
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
	server = server.Normalize()
	if err := server.Validate(); err != nil {
		return nil, err
	}
	if f.Secrets == nil {
		return nil, errors.New("SSH 敏感信息存储未初始化")
	}
	secrets, err := f.Secrets.Get(ctx, server.PrivateKeyRef)
	if err != nil {
		return nil, fmt.Errorf("读取 SSH 私钥失败：%w", err)
	}
	privateKey := strings.TrimSpace(secrets["private_key"])
	if privateKey == "" {
		return nil, errors.New("SSH 私钥内容不能为空")
	}
	var signer ssh.Signer
	passphrase := secrets["passphrase"]
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(privateKey))
	}
	if err != nil {
		return nil, errors.New("解析 SSH 私钥失败：私钥格式或口令无效")
	}
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
	sshClient, err := connectSSHClient(timeoutContext, server, signer, algorithms)
	if err != nil {
		return nil, err
	}
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, fmt.Errorf("创建 SFTP 客户端失败：%w", err)
	}
	return &sshClientAdapter{ssh: sshClient, sftp: sftpClient}, nil
}

func (f SSHFactory) secureHostKeyAlgorithms() ([]string, error) {
	supported := ssh.SupportedAlgorithms().HostKeys
	if len(f.HostKeyAlgorithms) == 0 {
		return append([]string(nil), supported...), nil
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
	signer ssh.Signer,
	algorithms []string,
) (*ssh.Client, error) {
	address := net.JoinHostPort(server.Address, fmt.Sprintf("%d", server.Port))
	var mismatchErrors []string
	for _, algorithm := range algorithms {
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
			Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyAlgorithms: []string{algorithm},
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
		clientConn, channels, requests, handshakeErr := ssh.NewClientConn(connection, address, config)
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
		if errors.As(handshakeErr, &negotiationError) && negotiationError.What == "host key" {
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
	ssh  *ssh.Client
	sftp *sftp.Client
}

func (c *sshClientAdapter) MkdirAll(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.sftp.MkdirAll(path)
}

func (c *sshClientAdapter) Exists(ctx context.Context, path string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, err := c.sftp.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (c *sshClientAdapter) Upload(ctx context.Context, path string, data []byte, mode uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := c.sftp.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	closeFile := func() error { return file.Close() }
	if _, err := file.Write(data); err != nil {
		_ = closeFile()
		return err
	}
	if err := file.Chmod(os.FileMode(mode)); err != nil {
		_ = closeFile()
		return err
	}
	return closeFile()
}

func (c *sshClientAdapter) Rename(ctx context.Context, source, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.sftp.Rename(source, target)
}

func (c *sshClientAdapter) Remove(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := c.sftp.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (c *sshClientAdapter) Run(ctx context.Context, command []string) (CommandResult, error) {
	if len(command) == 0 {
		return CommandResult{}, errors.New("远程命令不能为空")
	}
	if err := validateCommand(command); err != nil {
		return CommandResult{}, err
	}
	session, err := c.ssh.NewSession()
	if err != nil {
		return CommandResult{}, err
	}
	defer session.Close()
	output, err := session.CombinedOutput(joinShellArgs(command))
	if err != nil {
		var exitError *ssh.ExitError
		if errors.As(err, &exitError) {
			return CommandResult{Output: output, ExitCode: exitError.ExitStatus()}, nil
		}
		return CommandResult{Output: output, ExitCode: -1}, err
	}
	if err := ctx.Err(); err != nil {
		return CommandResult{Output: output, ExitCode: -1}, err
	}
	return CommandResult{Output: output}, nil
}

func (c *sshClientAdapter) Close() error {
	var first error
	if c.sftp != nil {
		if err := c.sftp.Close(); err != nil {
			first = err
		}
	}
	if c.ssh != nil {
		if err := c.ssh.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func validateCommand(command []string) error {
	for _, token := range command {
		if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\x00\r\n") {
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
