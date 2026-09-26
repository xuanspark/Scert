package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"

	"git.pcux.cn/xuans/Scert/backend/internal/certstorage"
)

func run(ctx context.Context) error {
	root := flag.String("root", "/ssl", "安装时额外批准的证书存储根目录")
	flag.Parse()
	if flag.NArg() != 0 || os.Geteuid() != 0 || os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) || os.Getenv("LISTEN_FDS") != "1" {
		return errors.New("目录服务必须由 root 的单个 systemd socket 启动")
	}
	account, err := user.Lookup("Scert")
	if err != nil {
		return err
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return err
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return err
	}
	file := os.NewFile(3, "storage-socket")
	if file == nil {
		return errors.New("目录服务监听描述符不存在")
	}
	defer file.Close()
	listener, err := net.FileListener(file)
	if err != nil {
		return err
	}
	defer listener.Close()
	local, ok := listener.(*net.UnixListener)
	if !ok || listener.Addr().Network() != "unix" || listener.Addr().String() != certstorage.SocketPath {
		return errors.New("目录服务拒绝非固定 Unix 通道")
	}
	return (certstorage.Service{
		UID: uint32(uid), GID: uint32(gid), Roots: []string{"/ssl", "/var/lib/scert/certificates", *root},
		Log: func(err error) { fmt.Fprintln(os.Stderr, "证书目录自动准备失败：", err) },
	}).Serve(ctx, local)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
