//go:build windows

package localreset

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const DefaultPath = `\\.\pipe\scert-admin-control`
const pipeSDDL = "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)"

func pipePath(path string) (string, error) {
	if path == "" {
		path = DefaultPath
	}
	prefix := `\\.\pipe\`
	if !strings.HasPrefix(path, prefix) || len(path) <= len(prefix) ||
		strings.ContainsAny(strings.TrimPrefix(path, prefix), `\/:`) {
		return "", ErrUnavailable
	}
	return path, nil
}

func Listen(path string) (*Server, error) {
	return listenPipe(path, pipeSDDL)
}

func listenPipe(path, sddl string) (*Server, error) {
	path, err := pipePath(path)
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		InputBufferSize:    MaxFrameSize,
		OutputBufferSize:   MaxFrameSize,
	})
	if err != nil {
		return nil, err
	}
	return newServer(listener, func(conn net.Conn) (Peer, error) {
		return pipePeer(conn, false)
	}, nil), nil
}

// pipePeer 除管道 ACL 外再次验证进程令牌，拒绝非提升管理员及网络登录令牌。
func pipePeer(conn net.Conn, server bool) (Peer, error) {
	provider, ok := conn.(interface{ Fd() uintptr })
	if !ok || provider.Fd() == 0 {
		return Peer{}, ErrUnauthorized
	}
	var pid uint32
	var err error
	if server {
		err = windows.GetNamedPipeServerProcessId(windows.Handle(provider.Fd()), &pid)
	} else {
		err = windows.GetNamedPipeClientProcessId(windows.Handle(provider.Fd()), &pid)
	}
	if err != nil || pid == 0 {
		return Peer{}, ErrUnauthorized
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Peer{}, ErrUnauthorized
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token) != nil {
		return Peer{}, ErrUnauthorized
	}
	defer token.Close()
	return tokenPeer(token)
}

func tokenPeer(token windows.Token) (Peer, error) {
	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return Peer{}, ErrUnauthorized
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return Peer{}, ErrUnauthorized
	}
	network, err := windows.CreateWellKnownSid(windows.WinNetworkSid)
	if err != nil {
		return Peer{}, ErrUnauthorized
	}
	admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return Peer{}, ErrUnauthorized
	}
	enabledAdmin := false
	for _, group := range groups.AllGroups() {
		if group.Sid.Equals(network) {
			return Peer{}, ErrUnauthorized
		}
		if group.Sid.Equals(admin) && group.Attributes&windows.SE_GROUP_ENABLED != 0 &&
			group.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY == 0 {
			enabledAdmin = true
		}
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return Peer{}, ErrUnauthorized
	}
	if !user.User.Sid.Equals(system) && (!enabledAdmin || !token.IsElevated()) {
		return Peer{}, ErrUnauthorized
	}
	return Peer{Identity: "sid:" + user.User.Sid.String()}, nil
}

// expectedUID 仅用于 Unix；Windows 通过 SYSTEM 或提升管理员令牌识别服务。
func Dial(ctx context.Context, path string, expectedUID uint32) (net.Conn, error) {
	path, err := pipePath(path)
	if err != nil {
		return nil, err
	}
	conn, err := winio.DialPipeContext(ctx, path)
	if err != nil {
		return nil, err
	}
	if _, err := pipePeer(conn, true); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func AuthorizeCurrentProcess() error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return ErrUnauthorized
	}
	defer token.Close()
	_, err = tokenPeer(token)
	return err
}
