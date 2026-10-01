package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/admin"
	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/httpapi"
	"git.pcux.cn/xuans/Scert/backend/internal/logging"
	"git.pcux.cn/xuans/Scert/backend/internal/notification"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
	"git.pcux.cn/xuans/Scert/backend/internal/sshkey"
)

type App struct {
	Config          config.Config
	Logger          *logging.Logger
	Database        *database.Database
	Repository      *repository.SQLDomainRepository
	Servers         *repository.SQLServerRepository
	Users           *repository.SQLUserRepository
	Tasks           *repository.SQLTaskRepository
	Deployment      *deployment.Service
	SecretStore     *secretstore.Store
	SSHKeys         *sshkey.Service
	DNSVerification *setup.DNSVerificationManager
	Certificates    *certificate.Service
	Workflow        *CertificateWorkflow
	Renewal         *certificate.RenewalScheduler
	HTTP            *httpapi.Server
	maintenanceStop context.CancelFunc
	maintenanceDone chan struct{}
	managementStop  context.CancelFunc
	managementDone  chan struct{}
}

const (
	auditLogRetentionDays = 30
	auditCleanupInterval  = 24 * time.Hour
)

func New(ctx context.Context, cfg config.Config) (*App, error) {
	return NewWithConfigPath(ctx, cfg, filepath.Join(cfg.Paths.Config, "scert.json"))
}

func NewWithConfigPath(ctx context.Context, cfg config.Config, configPath string) (*App, error) {
	if !cfg.SetupReady {
		return nil, fmt.Errorf("系统尚未初始化，请先启动 scert serve 完成首次配置")
	}
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
	secretStore, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		_ = logger.Close()
		return nil, fmt.Errorf("初始化敏感信息存储失败：%w", err)
	}
	runtimeDatabaseConfig, err := database.ResolveConfig(ctx, cfg.Database, secretStore)
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	db, err := database.Open(ctx, runtimeDatabaseConfig)
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	if err := database.Migrate(ctx, db.DB, runtimeDatabaseConfig.Driver); err != nil {
		_ = db.Close()
		_ = logger.Close()
		return nil, err
	}
	dnsTester := dnsprovider.NewTester(dnsprovider.NewLegoFactory())
	dnsVerification := setup.NewDNSVerificationManager(dnsTester, secretStore)
	repo := repository.NewSQLDomainRepository(db.DB, runtimeDatabaseConfig.Driver)
	serverRepo := repository.NewSQLServerRepository(db.DB, runtimeDatabaseConfig.Driver)
	userRepo := repository.NewSQLUserRepository(db.DB, runtimeDatabaseConfig.Driver)
	if cfg.SetupReady && cfg.Admin.Username != "" && cfg.Admin.PasswordHash != "" {
		if err := userRepo.EnsureSuperAdmin(ctx, repository.User{ID: "primary-super-admin", Username: cfg.Admin.Username, Email: cfg.Admin.Email, PasswordHash: cfg.Admin.PasswordHash}); err != nil {
			_ = db.Close()
			_ = logger.Close()
			return nil, fmt.Errorf("初始化超级管理员用户失败：%w", err)
		}
	}
	taskRepo := repository.NewSQLTaskRepository(db.DB, runtimeDatabaseConfig.Driver)
	transactionRepo := repository.NewSQLDeploymentTransactionRepository(db.DB, runtimeDatabaseConfig.Driver)
	deploymentService := deployment.NewServiceWithJournal(deployment.SSHFactory{Secrets: secretStore}, transactionRepo)
	remoteValidator := certificate.RemoteValidator{}
	deploymentService.VerifyRecoveryTarget = func(ctx context.Context, server deployment.Server, baseline deployment.RecoveryBaseline, committed bool) error {
		expected := baseline.PreviousFingerprint
		if committed {
			expected = baseline.ExpectedFingerprint
		}
		if expected == "" || baseline.Endpoint == "" || len(baseline.Domains) == 0 {
			return fmt.Errorf("恢复站点基线不完整")
		}
		validation, err := remoteValidator.ValidateHTTPSAt(ctx, baseline.Endpoint, server.Address, baseline.Domains)
		if err != nil {
			return err
		}
		if validation.FingerprintSHA256 != expected {
			return fmt.Errorf("站点证书指纹不符合事务基线")
		}
		return nil
	}
	if err := deploymentService.RecoverPending(ctx, serverRepo.GetServerByID); err != nil {
		logger.Error("恢复中断部署事务失败，已保留现场等待人工核查", "error", err)
	}
	if recovered, err := taskRepo.RecoverInterruptedTasks(ctx, time.Now().UTC()); err != nil {
		_ = db.Close()
		_ = logger.Close()
		return nil, fmt.Errorf("\u6062\u590d\u670d\u52a1\u91cd\u542f\u524d\u672a\u5b8c\u6210\u4efb\u52a1\u5931\u8d25\uff1a%w", err)
	} else if recovered > 0 {
		logger.Warn("\u670d\u52a1\u91cd\u542f\u524d\u672a\u5b8c\u6210\u4efb\u52a1\u5df2\u6807\u8bb0\u4e3a\u5931\u8d25", "count", recovered)
	}
	auditRepo := repository.NewSQLAuditRepository(db.DB, runtimeDatabaseConfig.Driver)
	if deleted, err := auditRepo.PruneExpiredAuditLogs(ctx, time.Now().UTC().AddDate(0, 0, -auditLogRetentionDays)); err != nil {
		logger.Warn("启动时清理过期审计日志失败", "error", err)
	} else if deleted > 0 {
		logger.Info("启动时已清理过期审计日志", "count", deleted)
	}
	sshKeys := sshkey.NewService(secretStore)
	accounts := certificate.NewAccountRepository(secretStore)
	certificates := certificate.NewService(cfg, repo, secretStore, dnsprovider.NewLegoFactory(), accounts)
	aliyunAuthChecker := dnsprovider.NewAliyunAuthorizationChecker()
	certificates.SetDNSAuthorizationChecker(aliyunAuthChecker)

	notifier := notification.New(cfg.Notification, secretStore)
	workflow := &CertificateWorkflow{
		Certificates: certificates,
		Domains:      repo,
		Servers:      serverRepo,
		Deployment:   deploymentService,
		RemoteVerifierForServer: func(ctx context.Context, server deployment.Server, endpoint string, expectedDomains []string) (certificate.ValidationResult, error) {
			return remoteValidator.ValidateHTTPSAt(ctx, endpoint, server.Address, expectedDomains)
		},
		Notifier:           notifier,
		NotifyError:        func(err error) { logger.Error("发送邮件通知失败", "error", err) },
		CurrentCertificate: certificates.Current,
	}
	var server *httpapi.Server
	management := &managementRenewal{
		readState: func() (httpapi.ManagementCertificateState, error) { return server.ManagementCertificate() },
		issue:     certificates.IssueForSetup,
		tasks:     taskRepo, audit: auditRepo, notifier: notifier,
		notifyError: func(err error) { logger.Error("管理证书续期或通知失败", "error", err) },
	}
	trackedWorkflow := &certificateTaskWorkflow{workflow: workflow, tasks: taskRepo, audit: auditRepo}
	trackedWorkflow.logError = func(err error) { logger.Error("证书续期或部署任务失败", "error", err) }
	renewal := certificate.NewRenewalSchedulerWithManagement(repo, trackedWorkflow, certificate.RenewalPolicy{
		CheckInterval:       time.Duration(cfg.ACME.RenewalCheckIntervalDays) * 24 * time.Hour,
		NearExpiryThreshold: time.Duration(cfg.ACME.RenewalNearExpiryDays) * 24 * time.Hour,
		RenewalThreshold:    time.Duration(cfg.ACME.RenewalThresholdDays) * 24 * time.Hour,
		UrgentThreshold:     time.Duration(cfg.ACME.RenewalUrgentThresholdDays) * 24 * time.Hour,
		MaxRetries:          cfg.ACME.RenewalMaxRetries,
		RetryInterval:       time.Duration(cfg.ACME.RenewalRetryIntervalMins) * time.Minute,
	}, management)
	server = httpapi.New(httpapi.Dependencies{
		Config: cfg, ConfigPath: configPath, Logger: logger, Repository: repo, Servers: serverRepo, Users: userRepo, SSHKeys: sshKeys, DeploymentFactory: deployment.SSHFactory{Secrets: secretStore},
		ProjectMembers:   repository.NewSQLProjectMemberRepository(db.DB, runtimeDatabaseConfig.Driver),
		ProjectDirectory: repository.NewSQLProjectMemberRepository(db.DB, runtimeDatabaseConfig.Driver),
		ProjectLifecycle: repository.NewSQLProjectMemberRepository(db.DB, runtimeDatabaseConfig.Driver),
		ProjectResources: repository.NewSQLProjectMemberRepository(db.DB, runtimeDatabaseConfig.Driver),
		ProjectBindings:  repository.NewSQLProjectMemberRepository(db.DB, runtimeDatabaseConfig.Driver),
		ProjectDomains:   repository.NewSQLProjectMemberRepository(db.DB, runtimeDatabaseConfig.Driver),
		SetupDNS:         dnsVerification, Secrets: secretStore, Firewall: firewall.NewManager(), AliyunChecker: aliyunAuthChecker,
		IssueCertificate: workflow.IssueForHTTP,
		IssueSetupCertificate: func(ctx context.Context, request certificate.SetupCertificateRequest) (certificate.SavedResource, error) {
			return certificates.IssueForSetup(ctx, request)
		},
		RollbackSetupCertificate: func(ctx context.Context, saved certificate.SavedResource) error {
			return certificate.RollbackSavedResource(ctx, saved)
		},
		ImportCertificate: func(ctx context.Context, request certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error) {
			result, err := trackedWorkflow.Import(ctx, request)
			return result.Certificate, result.Deployment, err
		},
		DeployCertificate: func(ctx context.Context, domainID string) (certificate.IssueResult, deployment.Report, error) {
			result, err := trackedWorkflow.DeployCurrent(ctx, domainID)
			return result.Certificate, result.Deployment, err
		},
		RestoreCertificateVersion: func(ctx context.Context, domainID, version string) (certificate.IssueResult, error) {
			result, err := trackedWorkflow.Restore(ctx, domainID, version)
			return result.Certificate, err
		},
		ValidateCertificate: func(ctx context.Context, domainID string) (certificate.IssueResult, certificate.ValidationResult, error) {
			return trackedWorkflow.Validate(ctx, domainID)
		},
		RunRenewal: func(ctx context.Context) (certificate.RenewalReport, error) {
			return renewal.RunOnce(ctx)
		},
		UpdateRenewalPolicy: renewal.UpdatePolicy,
		CurrentCertificate:  certificates,
		ResetStatePath:      admin.StatePath(cfg.Paths.Data),
		Audit:               auditRepo,
		Tasks:               taskRepo,
		ReadinessCheck:      db.DB.PingContext,
	})
	activator, recoverManagement, err := newManagementActivator(cfg, server)
	if err != nil {
		_ = db.Close()
		_ = logger.Close()
		return nil, fmt.Errorf("初始化管理证书协调器失败：%w", err)
	}
	management.activator = activator
	application := &App{
		Config: cfg, Logger: logger, Database: db, Repository: repo,
		Servers:     serverRepo,
		Users:       userRepo,
		Tasks:       taskRepo,
		Deployment:  deploymentService,
		SecretStore: secretStore, SSHKeys: sshKeys, DNSVerification: dnsVerification, HTTP: server,
		Certificates: certificates,
		Workflow:     workflow,
		Renewal:      renewal,
	}
	application.startAuditCleanup(auditRepo)
	application.startManagementRecovery(recoverManagement)
	return application, nil
}

func (a *App) startAuditCleanup(repo *repository.SQLAuditRepository) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.maintenanceStop = cancel
	a.maintenanceDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(auditCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				deleted, err := repo.PruneExpiredAuditLogs(ctx, time.Now().UTC().AddDate(0, 0, -auditLogRetentionDays))
				if err != nil {
					a.Logger.Warn("定时清理过期审计日志失败", "error", err)
					continue
				}
				if deleted > 0 {
					a.Logger.Info("已定时清理过期审计日志", "count", deleted)
				}
			}
		}
	}()
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	var first error
	if a.managementStop != nil {
		a.managementStop()
	}
	if a.Renewal != nil {
		a.Renewal.Stop()
	}
	if a.maintenanceStop != nil {
		a.maintenanceStop()
	}
	if a.maintenanceDone != nil {
		<-a.maintenanceDone
	}
	if a.managementDone != nil {
		<-a.managementDone
	}
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
