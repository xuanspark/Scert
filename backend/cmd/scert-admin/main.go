package main

import (
	"context"
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
	controller := admin.New(cfg)
	session, err := controller.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "启用密码重置入口失败：", err)
		os.Exit(1)
	}
	fmt.Println("Scert-admin 已启用临时密码重置入口，不监听独立网络端口。")
	fmt.Println("重置地址：", session.State.ResetURL)
	fmt.Println("一次性校验码：", session.Code)
	fmt.Println("停止 Scert-admin 后入口将立即关闭；异常退出时入口会在租约过期后自动关闭。")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(admin.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := controller.Stop(); err != nil {
				fmt.Fprintln(os.Stderr, "关闭密码重置入口失败：", err)
				os.Exit(1)
			}
			fmt.Println("Scert-admin 已关闭密码重置入口。")
			return
		case <-ticker.C:
			if err := controller.Refresh(); err != nil {
				fmt.Fprintln(os.Stderr, "续期密码重置入口租约失败：", err)
				_ = controller.Stop()
				os.Exit(1)
			}
		}
	}
}
