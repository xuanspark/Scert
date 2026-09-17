package nginxactivation

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var ErrLocalOperation = errors.New("本地 Nginx 激活操作未完成，请核对事务状态")

// Client 只能连接受保护的本地 Unix socket；生产调用方必须指定 root 的 UID 0。
type Client struct {
	SocketPath string
	ServiceUID uint32
}

func (c Client) Ready(ctx context.Context) (ReadyState, error) {
	result, err := c.call(ctx, request{Action: "ready"})
	if err != nil {
		return ReadyState{}, err
	}
	if result.Ready == nil {
		return ReadyState{}, errors.New("本地激活服务没有返回就绪状态")
	}
	return *result.Ready, nil
}

func (c Client) State(ctx context.Context) (State, error) {
	result, err := c.call(ctx, request{Action: "state"})
	if err != nil {
		return State{}, err
	}
	if result.State == nil {
		return State{}, errors.New("本地激活服务没有返回事务状态")
	}
	return *result.State, nil
}

func (c Client) Stage(ctx context.Context, chain, key []byte) (string, error) {
	result, err := c.call(ctx, request{Action: "stage", Chain: chain, Key: key})
	if err == nil && !validID(result.ID) {
		return "", errors.New("本地激活服务返回无效事务编号")
	}
	return result.ID, err
}

func (c Client) Commit(ctx context.Context, id string) error {
	_, err := c.call(ctx, request{Action: "commit", ID: id})
	return err
}

func (c Client) Reconcile(ctx context.Context, id string) (State, error) {
	result, err := c.call(ctx, request{Action: "reconcile", ID: id})
	if result.State != nil {
		return *result.State, err
	}
	if err != nil {
		return State{}, err
	}
	return State{}, errors.New("本地激活服务没有返回恢复结果")
}

func (c Client) call(ctx context.Context, message request) (response, error) {
	message.Version = protocolVersion
	if !validRequest(message) {
		return response{}, errors.New("本地激活请求无效")
	}
	if err := validateSocket(c.SocketPath, c.ServiceUID); err != nil {
		return response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 95*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return response{}, err
	}
	defer connection.Close()
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return response{}, errors.New("本地激活连接类型无效")
	}
	uid, err := peerUID(unixConnection)
	if err != nil || uid != c.ServiceUID {
		return response{}, errors.New("本地激活服务身份不可信")
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := connection.SetDeadline(deadline); err != nil {
		return response{}, err
	}
	if err := writeFrame(connection, message); err != nil {
		return response{}, errors.Join(err, ctx.Err())
	}
	var result response
	if err := readFrame(connection, &result); err != nil {
		return response{}, errors.Join(err, ctx.Err())
	}
	if result.Version != protocolVersion {
		return response{}, errors.New("本地激活协议版本不匹配")
	}
	if result.Code != "ok" {
		return result, ErrLocalOperation
	}
	return result, nil
}

func validateSocket(path string, uid uint32) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return errors.New("激活 socket 必须使用规范绝对路径")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || stat.Uid != uid || info.Mode().Perm()&0o117 != 0 {
		return errors.New("激活 socket 类型、所有者或权限不可信")
	}
	return validateTrustedParents(filepath.Dir(path), uid)
}

func validateTrustedParents(path string, uid uint32) error {
	for ; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || (stat.Uid != uid && stat.Uid != 0) {
			return errors.New("激活策略父目录的类型或所有者不可信")
		}
		if info.Mode().Perm()&0o022 != 0 && !(stat.Uid == 0 && info.Mode()&os.ModeSticky != 0) {
			return errors.New("激活策略父目录允许非受信用户写入")
		}
		if filepath.Dir(path) == path {
			return nil
		}
	}
}
