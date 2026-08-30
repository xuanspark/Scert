package service

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/app"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type Runtime struct {
	core   *app.App
	cancel context.CancelFunc
}

func Open(configPath, baseDir string) (*Runtime, error) {
	configPath = strings.TrimSpace(configPath)
	baseDir = strings.TrimSpace(baseDir)
	if configPath == "" {
		configPath = filepath.Join(defaultPaths(baseDir).Config, "scert.json")
	}
	if baseDir == "" {
		baseDir = filepath.Dir(filepath.Dir(configPath))
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		cfg = defaultConfig(baseDir)
	}

	ctx, cancel := context.WithCancel(context.Background())
	core, err := app.NewWithConfigPath(ctx, cfg, configPath)
	if err != nil {
		cancel()
		return nil, err
	}
	if core.Renewal != nil && cfg.SetupReady {
		core.Renewal.Start(ctx)
	}
	return &Runtime{core: core, cancel: cancel}, nil
}

func (r *Runtime) Handler() http.Handler {
	if r == nil || r.core == nil || r.core.HTTP == nil {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "Scert 核心服务未初始化", http.StatusServiceUnavailable)
		})
	}
	return r.core.HTTP.Handler()
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	if r.core != nil {
		return r.core.Close()
	}
	return nil
}

func defaultConfig(baseDir string) config.Config {
	cfg := config.Default(baseDir)
	if runtime.GOOS == "linux" && strings.TrimSpace(baseDir) != "" {
		install := filepath.Join(baseDir, "Scert")
		cfg.Paths.Install = install
		cfg.Paths.Certificate = filepath.Join(install, "ssl")
		cfg.Paths.Config = filepath.Join(install, "config")
		cfg.Paths.Data = filepath.Join(install, "data")
		cfg.Paths.Log = filepath.Join(install, "logs")
		cfg.Paths.Backup = filepath.Join(install, "backups")
		cfg.Database.DSN = filepath.Join(cfg.Paths.Data, "scert.db")
	}
	return cfg
}

func defaultPaths(baseDir string) config.PathsConfig {
	return defaultConfig(baseDir).Paths
}
