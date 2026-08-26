package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/admin"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func main() {
	baseDir := flag.String("base-dir", "", "Windows 默认安装根目录")
	configPath := flag.String("config", "", "配置文件路径")
	flag.Parse()
	path := *configPath
	if path == "" {
		path = filepath.Join(config.DefaultPaths(*baseDir).Config, "scert.json")
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取配置失败：", err)
		os.Exit(1)
	}
	server := admin.New(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintln(os.Stderr, "停止 Scert-admin 失败：", err)
		}
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "Scert-admin 退出：", err)
			os.Exit(1)
		}
	}
}
