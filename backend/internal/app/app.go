package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/httpapi"
	"git.pcux.cn/xuans/Scert/backend/internal/logging"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type App struct {
	Config     config.Config
	Logger     *logging.Logger
	Database   *database.Database
	Repository *repository.SQLDomainRepository
	HTTP       *httpapi.Server
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	for _, path := range []string{cfg.Paths.Install, cfg.Paths.Certificate, cfg.Paths.Config, cfg.Paths.Data, cfg.Paths.Log, cfg.Paths.Backup} {
		if err := makeDirectory(path); err != nil {
			return nil, fmt.Errorf("创建运行目录失败：%w", err)
		}
	}
	logger, err := logging.New(cfg.Paths.Log, slog.LevelInfo)
	if err != nil {
		return nil, fmt.Errorf("初始化日志失败：%w", err)
	}
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	if err := database.Migrate(ctx, db.DB, cfg.Database.Driver); err != nil {
		_ = db.Close()
		_ = logger.Close()
		return nil, err
	}
	repo := repository.NewSQLDomainRepository(db.DB)
	server := httpapi.New(httpapi.Dependencies{Config: cfg, Logger: logger, Repository: repo})
	return &App{Config: cfg, Logger: logger, Database: db, Repository: repo, HTTP: server}, nil
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	var first error
	if a.HTTP != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := a.HTTP.Shutdown(ctx); err != nil && first == nil {
			first = err
		}
		cancel()
	}
	if a.Database != nil {
		if err := a.Database.Close(); err != nil && first == nil {
			first = err
		}
	}
	if a.Logger != nil {
		if err := a.Logger.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func makeDirectory(path string) error {
	return osMkdirAll(path)
}
