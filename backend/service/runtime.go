package service

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"git.pcux.cn/xuans/Scert/backend/internal/app"
	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/httpapi"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

type Runtime struct {
	mu         sync.RWMutex
	closed     bool
	ctx        context.Context
	configPath string
	core       *app.App
	cancel     context.CancelFunc
	initial    http.Handler
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
		if err != nil {
			cancel()
			return nil, err
		}
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
		return &Runtime{ctx: ctx, configPath: configPath, cancel: cancel, initial: handler, initialDNS: dns}, nil
	}
	core, err := app.NewWithConfigPath(ctx, cfg, configPath)
	if err != nil {
		cancel()
		return nil, err
	}
	if core.Renewal != nil && cfg.SetupReady {
		core.Renewal.Start(ctx)
	}
	return &Runtime{ctx: ctx, configPath: configPath, core: core, cancel: cancel}, nil
}

func (r *Runtime) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if r == nil || r.ctx == nil {
			http.Error(writer, "Scert 核心服务未初始化", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithCancel(request.Context())
		stop := context.AfterFunc(r.ctx, cancel)
		defer stop()
		defer cancel()
		request = request.WithContext(ctx)
		r.mu.RLock()
		if !r.closed && r.core != nil {
			defer r.mu.RUnlock()
			r.core.HTTP.Handler().ServeHTTP(writer, request)
			return
		}
		r.mu.RUnlock()
		// 桌面初始化请求串行处理，保存后下一次请求才切换，避免关闭在途验证的凭据。
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.closed {
			http.Error(writer, "Scert 核心服务已关闭", http.StatusServiceUnavailable)
			return
		}
		if r.core == nil {
			if err := r.activateCompletedSetup(); err != nil {
				http.Error(writer, "初始化配置已保存，但核心服务启动失败，请检查配置和运行目录后重试", http.StatusServiceUnavailable)
				return
			}
		}
		if r.core != nil {
			r.core.HTTP.Handler().ServeHTTP(writer, request)
			return
		}
		if r.initial == nil {
			http.Error(writer, "Scert 核心服务未初始化", http.StatusServiceUnavailable)
			return
		}
		r.initial.ServeHTTP(writer, request)
	})
}

// 调用方持有运行时写锁；不重新创建窗口，原有处理器即可切换到正式服务。
func (r *Runtime) activateCompletedSetup() error {
	cfg, err := config.LoadWithEnv(r.configPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !cfg.SetupReady {
		return nil
	}
	core, err := app.NewWithConfigPath(r.ctx, cfg, r.configPath)
	if err != nil {
		return err
	}
	if core.Renewal != nil {
		core.Renewal.Start(r.ctx)
	}
	r.core = core
	r.initial = nil
	if r.initialDNS != nil {
		r.initialDNS.DiscardAllExcept(cfg.DNS.CredentialRef)
		r.initialDNS = nil
	}
	return nil
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	// 先取消长操作再等待处理器退出，避免关闭过程被初始化验证阻塞。
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.initialDNS != nil {
		r.initialDNS.DiscardAll()
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
