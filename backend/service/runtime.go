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
	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/httpapi"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

type Runtime struct {
	core   *app.App
	cancel context.CancelFunc
	initial http.Handler
	initialDNS *setup.DNSVerificationManager
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

	cfg, err := config.LoadWithEnv(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		cfg = defaultConfig(baseDir)
		if err := cfg.ApplyEnvironment(); err != nil {
			return nil, err
		}
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	if !cfg.SetupReady {
		// 桌面端保留本机向导，但不再为初始化预先创建正式数据库和后台任务。
		store, err := secretstore.Open(cfg.Paths.Config)
		if err != nil { cancel(); return nil, err }
		dns := setup.NewDNSVerificationManager(dnsprovider.NewTester(dnsprovider.NewLegoFactory()), store)
		issuer := certificate.NewService(cfg, nil, store, dnsprovider.NewLegoFactory(), certificate.NewAccountRepository(store))
		issuer.SetDNSAuthorizationChecker(dnsprovider.NewAliyunAuthorizationChecker())
		server := httpapi.New(httpapi.Dependencies{
			Config: cfg, ConfigPath: configPath, SetupDNS: dns, Secrets: store,
			Firewall: firewall.NewManager(), IssueSetupCertificate: issuer.IssueForSetup,
			RollbackSetupCertificate: certificate.RollbackSavedResource,
		})
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/api/v1/setup" || strings.HasPrefix(request.URL.Path, "/api/v1/setup/") {
				server.Handler().ServeHTTP(writer, request)
				return
			}
			http.NotFound(writer, request)
		})
		return &Runtime{cancel: cancel, initial: handler, initialDNS: dns}, nil
	}
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
	if r != nil && r.initial != nil { return r.initial }
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
	if r.initialDNS != nil { r.initialDNS.DiscardAll() }
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
