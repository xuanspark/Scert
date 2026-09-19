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
	"time"

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

func execute(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("scert-nginx", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "用法：scert-nginx [参数]；默认由 systemd socket 启动服务。")
		flags.PrintDefaults()
	}
	path := flags.String("policy", "/etc/scert-nginx/policy.json", "root 管理的独立激活策略文件")
	initialize := flags.Bool("initialize", false, "仅初始化专用证书材料，不重载 Nginx")
	chain := flags.String("certificate", "", "root 受保护的现有完整证书链文件")
	key := flags.String("private-key", "", "root 受保护的现有私钥文件")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *path == "" {
		return errors.New("不接受位置参数，策略路径不能为空")
	}
	if !*initialize {
		if *chain != "" || *key != "" {
			return errors.New("证书材料参数仅用于显式初始化")
		}
		return run(ctx, *path)
	}
	if *chain == "" || *key == "" {
		return errors.New("初始化必须同时提供证书和私钥路径")
	}
	if os.Geteuid() != 0 {
		return errors.New("Nginx 激活目录初始化必须由 root 执行")
	}
	policy, err := nginxactivation.LoadPolicy(*path)
	if err != nil {
		return fmt.Errorf("读取受信激活策略失败：%w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := policy.Rotation().Initialize(ctx, *chain, *key); err != nil {
		return err
	}
	fmt.Println("专用证书材料已初始化；尚未修改或重载 Nginx，请继续核对站点接线。")
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Nginx 本地激活服务启动或运行失败：", err)
		os.Exit(1)
	}
}
