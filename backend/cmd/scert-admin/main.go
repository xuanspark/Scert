package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/admin"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func writeSessionInfo(writer io.Writer, session admin.Session) {
	fmt.Fprintln(writer, "Scert-admin 已启用临时密码重置入口，不监听独立网络端口。")
	fmt.Fprintln(writer, "重置地址：", session.State.ResetURL)
	fmt.Fprintln(writer, "一次性校验码已写入受保护的临时文件：", session.State.CodePath)
	fmt.Fprintln(writer, "请使用 root 或 Scert 用户读取该文件；密码重置完成后文件会自动销毁。")
	fmt.Fprintln(writer, "停止 Scert-admin 后入口将立即关闭；异常退出时入口会在租约过期后自动关闭。")
}
func main() {
	baseDir := flag.String("base-dir", "", "Windows 默认安装根目录")
	configPath := flag.String("config", "", "配置文件路径")
	codeOutput := flag.String("code-output", "", "一次性校验码文件路径")
	flag.Parse()
	path := *configPath
	if path == "" {
		path = filepath.Join(config.DefaultPaths(*baseDir).Config, "scert.json")
	}
	cfg, err := config.LoadWithEnv(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取配置失败：", err)
		os.Exit(1)
	}
	controller := admin.NewWithCodePath(cfg, *codeOutput)
	session, err := controller.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "启用密码重置入口失败：", err)
		os.Exit(1)
	}
	writeSessionInfo(os.Stdout, session)
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
