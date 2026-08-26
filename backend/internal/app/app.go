package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/httpapi"
	"git.pcux.cn/xuans/Scert/backend/internal/logging"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

type App struct {
	Config          config.Config
	Logger          *logging.Logger
	Database        *database.Database
	Repository      *repository.SQLDomainRepository
	SecretStore     *secretstore.Store
	DNSVerification *setup.DNSVerificationManager
	Certificates    *certificate.Service
	Renewal         *certificate.RenewalScheduler
	HTTP            *httpapi.Server
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	return NewWithConfigPath(ctx, cfg, filepath.Join(cfg.Paths.Config, "scert.json"))
}

func NewWithConfigPath(ctx context.Context, cfg config.Config, configPath string) (*App, error) {
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
	secretStore, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		_ = db.Close()
		_ = logger.Close()
		return nil, fmt.Errorf("初始化敏感信息存储失败：%w", err)
	}
	dnsTester := dnsprovider.NewTester(dnsprovider.NewLegoFactory())
	dnsVerification := setup.NewDNSVerificationManager(dnsTester, secretStore)
	repo := repository.NewSQLDomainRepository(db.DB)
	accounts := certificate.NewAccountRepository(secretStore)
	certificates := certificate.NewService(cfg, repo, secretStore, dnsprovider.NewLegoFactory(), accounts)
	renewal := certificate.NewRenewalScheduler(repo, certificates, cfg.ACME.RenewalThresholdDays)
	server := httpapi.New(httpapi.Dependencies{
		Config: cfg, ConfigPath: configPath, Logger: logger, Repository: repo,
		SetupDNS: dnsVerification, Firewall: firewall.NewManager(),
	})
	return &App{
		Config: cfg, Logger: logger, Database: db, Repository: repo,
		SecretStore: secretStore, DNSVerification: dnsVerification, HTTP: server,
		Certificates: certificates,
		Renewal:      renewal,
	}, nil
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
