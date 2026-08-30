package main

import (
	"context"
	"embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/service"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	baseDir := resolveBaseDir()
	configPath := strings.TrimSpace(os.Getenv("SCERT_CONFIG_PATH"))
	if configPath == "" {
		configPath = filepath.Join(baseDir, "Scert", "config", "scert.json")
	}
	core, err := service.Open(configPath, baseDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动 Scert 桌面核心服务失败：", err)
		os.Exit(1)
	}

	if err := wails.Run(&options.App{
		Title:            "Scert SSL 证书管理",
		Width:            1440,
		Height:           900,
		MinWidth:         1100,
		MinHeight:        720,
		AssetServer:      &assetserver.Options{Assets: assets, Handler: desktopHandler(core.Handler())},
		BackgroundColour: &options.RGBA{R: 246, G: 248, B: 252, A: 1},
		OnShutdown: func(_ context.Context) {
			_ = core.Close()
		},
	}); err != nil {
		_ = core.Close()
		fmt.Fprintln(os.Stderr, "Scert 桌面程序退出失败：", err)
		os.Exit(1)
	}
}

func resolveBaseDir() string {
	if value := strings.TrimSpace(os.Getenv("SCERT_DESKTOP_BASE_DIR")); value != "" {
		return filepath.Clean(value)
	}
	if value, err := os.UserConfigDir(); err == nil && strings.TrimSpace(value) != "" {
		return filepath.Clean(value)
	}
	if value, err := os.Executable(); err == nil {
		return filepath.Dir(value)
	}
	return "."
}

func desktopHandler(core http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		if request.Method != http.MethodGet || isCorePath(path) {
			core.ServeHTTP(writer, request)
			return
		}
		http.NotFound(writer, request)
	})
}

func isCorePath(path string) bool {
	return path == "/api" ||
		strings.HasPrefix(path, "/api/") ||
		path == "/healthz" ||
		path == "/reset" ||
		strings.HasPrefix(path, "/reset/")
}
