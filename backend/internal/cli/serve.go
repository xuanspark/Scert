package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"git.pcux.cn/xuans/Scert/backend/internal/app"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"github.com/spf13/cobra"
)

type serveStage func(context.Context, config.Config) (bool, error)

// loadServeConfig 只允许真正不存在的文件进入新初始化；损坏、无权限或符号链接均失败关闭。
func loadServeConfig(path string, defaults config.Config) (config.Config, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return config.Config{}, errors.New("服务配置必须是普通文件")
		}
		return config.LoadWithEnv(path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, err
	}
	defaults.Paths.Config = filepath.Dir(path)
	if err := defaults.ApplyEnvironment(); err != nil {
		return config.Config{}, err
	}
	if err := defaults.Validate(); err != nil {
		return config.Config{}, err
	}
	return defaults, nil
}

func serveLifecycle(ctx context.Context, cfg config.Config, reload func() (config.Config, error), bootstrapStage, applicationStage serveStage) error {
	if !cfg.SetupReady {
		completed, err := bootstrapStage(ctx, cfg)
		if err != nil {
			return err
		}
		if !completed || ctx.Err() != nil {
			return nil
		}
		next, err := reload()
		if err != nil {
			return fmt.Errorf("重新读取正式配置失败：%w", err)
		}
		if !next.SetupReady {
			return errors.New("初始化服务已退出，但正式配置尚未完成；拒绝启动业务服务")
		}
		if filepath.Clean(next.Paths.Data) != filepath.Clean(cfg.Paths.Data) {
			return errors.New("初始化不能更改进程锁所在数据目录")
		}
		cfg = next
	}
	if ctx.Err() != nil {
		return nil
	}
	// 关闭 Web 静态页面不等于停用核心 API、桌面管理和后台续期服务。
	_, err := applicationStage(ctx, cfg)
	return err
}

func bootstrapFirewallMode(command *cobra.Command, value string) string {
	if !command.Flags().Changed("bootstrap-firewall-mode") {
		if environment, ok := os.LookupEnv("SCERT_BOOTSTRAP_FIREWALL_MODE"); ok {
			return environment
		}
	}
	return value
}

func runApplication(ctx context.Context, cfg config.Config, path string) (bool, error) {
	service, err := app.NewWithConfigPath(ctx, cfg, path)
	if err != nil {
		return false, err
	}
	defer service.Close()
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- service.HTTP.ListenAndServe() }()
	if service.Renewal != nil {
		service.Renewal.Start(ctx)
	}
	select {
	case <-ctx.Done():
		return false, nil
	case err := <-errorsChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return false, nil
		}
		return false, err
	}
}

func bootstrapURLPath(cfg config.Config) string {
	if runtime.GOOS == "linux" {
		return "/run/scert/bootstrap-url"
	}
	path, _ := filepath.Abs(filepath.Join(cfg.Paths.Data, "run", "bootstrap-url"))
	return path
}
