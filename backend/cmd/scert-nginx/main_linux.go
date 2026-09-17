package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"git.pcux.cn/xuans/Scert/backend/internal/nginxactivation"
)

func inheritedListener(expectedPath string) (*net.UnixListener, error) {
	if os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) || os.Getenv("LISTEN_FDS") != "1" {
		return nil, errors.New("必须由单个 systemd Unix socket 启动激活服务")
	}
	file := os.NewFile(3, "activation-socket")
	if file == nil {
		return nil, errors.New("激活 socket 描述符不存在")
	}
	defer file.Close()
	listener, err := net.FileListener(file)
	if err != nil {
		return nil, err
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		_ = listener.Close()
		return nil, errors.New("激活器拒绝非 Unix 监听器")
	}
	unixListener.SetUnlinkOnClose(false)
	if unixListener.Addr().Network() != "unix" || unixListener.Addr().String() != expectedPath {
		_ = unixListener.Close()
		return nil, errors.New("激活 socket 与固定策略不匹配")
	}
	return unixListener, nil
}

func run(ctx context.Context, path string) error {
	if os.Geteuid() != 0 {
		return errors.New("独立 Nginx 激活服务必须由 root 启动")
	}
	policy, err := nginxactivation.LoadPolicy(path)
	if err != nil {
		return fmt.Errorf("读取受信激活策略失败：%w", err)
	}
	listener, err := inheritedListener(policy.SocketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	service := nginxactivation.LocalService{Rotation: policy.Rotation(), AllowedUID: *policy.AllowedUID}
	return service.Serve(ctx, listener)
}

func main() {
	path := flag.String("policy", "/etc/scert-nginx/policy.json", "root 管理的独立激活策略文件")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *path); err != nil {
		fmt.Fprintln(os.Stderr, "Nginx 本地激活服务启动或运行失败：", err)
		os.Exit(1)
	}
}
