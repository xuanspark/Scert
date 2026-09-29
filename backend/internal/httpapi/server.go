package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/logging"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/sshkey"
)

type setupSecretWriter interface {
	Put(context.Context, string, map[string]string) error
	Delete(context.Context, string) error
}

type readinessSecretReader interface {
	Get(context.Context, string) (map[string]string, error)
}

type Dependencies struct {
	Config                    config.Config
	ConfigPath                string
	FrontendDir               string
	Logger                    *logging.Logger
	Repository                repository.DomainRepository
	Servers                   repository.ServerRepository
	Users                     repository.UserRepository
	ProjectMembers            repository.ProjectMemberRepository
	ProjectDirectory          repository.ProjectDirectoryRepository
	ProjectLifecycle          repository.ProjectLifecycleRepository
	ProjectResources          repository.ProjectResourceReader
	ProjectBindings           repository.ProjectBindingRepository
	ProjectDomains            repository.ProjectDomainRepository
	SSHKeys                   *sshkey.Service
	DeploymentFactory         deployment.ClientFactory
	SetupDNS                  setupDNSVerifier
	Secrets                   setupSecretWriter
	Firewall                  firewall.Manager
	IssueCertificate          func(context.Context, string) (certificate.IssueResult, deployment.Report, error)
	IssueSetupCertificate     func(context.Context, certificate.SetupCertificateRequest) (certificate.SavedResource, error)
	RollbackSetupCertificate  func(context.Context, certificate.SavedResource) error
	ImportCertificate         func(context.Context, certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error)
	DeployCertificate         func(context.Context, string) (certificate.IssueResult, deployment.Report, error)
	RestoreCertificateVersion func(context.Context, string, string) (certificate.IssueResult, error)

	RunRenewal          func(context.Context) (certificate.RenewalReport, error)
	UpdateRenewalPolicy func(certificate.RenewalPolicy)
	CurrentCertificate  certificateReader
	ResetStatePath      string
	Audit               repository.AuditRepository
	Tasks               repository.TaskRepository
	ReadinessCheck      func(context.Context) error
	AliyunChecker       aliyunScopeChecker
}

type Server struct {
	configMu                 sync.RWMutex
	config                   config.Config
	configPath               string
	frontendDir              string
	logger                   *logging.Logger
	repository               repository.DomainRepository
	servers                  repository.ServerRepository
	users                    repository.UserRepository
	projectMembers           repository.ProjectMemberRepository
	projectDirectory         repository.ProjectDirectoryRepository
	projectLifecycle         repository.ProjectLifecycleRepository
	projectResources         repository.ProjectResourceReader
	projectBindings          repository.ProjectBindingRepository
	projectDomains           repository.ProjectDomainRepository
	sshKeys                  *sshkey.Service
	deploymentFactory        deployment.ClientFactory
	setupDNS                 setupDNSVerifier
	secrets                  setupSecretWriter
	firewall                 firewall.Manager
	issueCertificate         func(context.Context, string) (certificate.IssueResult, deployment.Report, error)
	issueSetupCertificate    func(context.Context, certificate.SetupCertificateRequest) (certificate.SavedResource, error)
	rollbackSetupCertificate func(context.Context, certificate.SavedResource) error
	importCertificate        func(context.Context, certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error)
	deployCertificate        func(context.Context, string) (certificate.IssueResult, deployment.Report, error)
	restoreVersion           func(context.Context, string, string) (certificate.IssueResult, error)

	runRenewal          func(context.Context) (certificate.RenewalReport, error)
	updateRenewalPolicy func(certificate.RenewalPolicy)
	currentCertificate  certificateReader
	resetStatePath      string
	audit               repository.AuditRepository
	tasks               repository.TaskRepository
	readinessCheck      func(context.Context) error
	aliyunChecker       aliyunScopeChecker
	taskMu              sync.Mutex
	taskCancels         map[string]context.CancelFunc
	issueTaskMu         sync.Mutex
	setupMu             sync.Mutex
	resetMu             sync.Mutex
	resetClosedHash     string
	resetLimiter        *resetRateLimiter
	authSessions        map[string]authSession
	authMu              sync.RWMutex
	loginAttempts       map[string]loginAttempt
	loginMu             sync.Mutex
	loginSources        map[string]loginSource
	loginPending        map[string]int
	loginInFlight       int
	loginPrunedAt       time.Time
	now                 func() time.Time
	httpMu              sync.Mutex
	httpStopped         bool
	httpServer          *http.Server
	httpListening       chan struct{}
	tlsMu               sync.Mutex
	tlsCertificate      atomic.Pointer[tls.Certificate]
	tlsRoots            *x509.CertPool
	router              *gin.Engine
}

func New(deps Dependencies) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	frontendDir := strings.TrimSpace(deps.FrontendDir)
	if frontendDir == "" {
		frontendDir = filepath.Join(deps.Config.Paths.Install, "web")
	}
	server := &Server{
		httpListening: make(chan struct{}),
		config:        deps.Config, configPath: deps.ConfigPath, frontendDir: filepath.Clean(frontendDir), logger: deps.Logger,
		repository: deps.Repository, setupDNS: deps.SetupDNS, secrets: deps.Secrets, firewall: deps.Firewall, router: router,
		servers:                  deps.Servers,
		users:                    deps.Users,
		projectMembers:           deps.ProjectMembers,
		projectDirectory:         deps.ProjectDirectory,
		projectLifecycle:         deps.ProjectLifecycle,
		projectResources:         deps.ProjectResources,
		projectBindings:          deps.ProjectBindings,
		projectDomains:           deps.ProjectDomains,
		sshKeys:                  deps.SSHKeys,
		deploymentFactory:        deps.DeploymentFactory,
		issueCertificate:         deps.IssueCertificate,
		issueSetupCertificate:    deps.IssueSetupCertificate,
		rollbackSetupCertificate: deps.RollbackSetupCertificate,
		importCertificate:        deps.ImportCertificate,
		deployCertificate:        deps.DeployCertificate,
		restoreVersion:           deps.RestoreCertificateVersion,
		runRenewal:               deps.RunRenewal,
		updateRenewalPolicy:      deps.UpdateRenewalPolicy,
		currentCertificate:       deps.CurrentCertificate,
		resetStatePath:           deps.ResetStatePath,
		audit:                    deps.Audit,
		tasks:                    deps.Tasks,
		readinessCheck:           deps.ReadinessCheck,
		aliyunChecker:            deps.AliyunChecker,
		taskCancels:              make(map[string]context.CancelFunc),
		resetLimiter:             newResetRateLimiter(),
		authSessions:             make(map[string]authSession),
		loginAttempts:            make(map[string]loginAttempt),
		loginSources:             make(map[string]loginSource),
		loginPending:             make(map[string]int),
		now:                      time.Now,
	}
	router.Use(server.requestMiddleware())
	server.registerRoutes()
	server.registerSetupRoutes()
	return server
}

func (s *Server) registerRoutes() {
	// 主路由保持独立，初始化路由由专用模块注册。
	s.router.GET("/healthz", s.health)
	s.router.GET("/readyz", s.ready)
	s.router.GET("/api/v1/status", s.status)
	s.router.GET("/api/v1/auth/session", s.authSession)
	s.router.POST("/api/v1/auth/login", s.requireSameOrigin, s.authLogin)
	s.router.POST("/api/v1/auth/logout", s.requireSameOrigin, s.authLogout)
	s.router.POST("/api/v1/auth/password", s.requireAuthenticated, s.changePassword)
	s.router.POST("/api/v1/password-reset/resolve", s.resolvePasswordResetAccount)
	adminAPI := s.router.Group("/api/v1", s.requireAdmin)
	userAPI := s.router.Group("/api/v1/users", s.requireAuthenticated)
	userAPI.GET("", s.listUsers)
	userAPI.POST("", s.requireSuperAdmin, s.createUser)
	userAPI.GET("/:id", s.getUser)
	userAPI.PUT("/:id", s.requireSuperAdmin, s.updateUser)
	userAPI.DELETE("/:id", s.requireSuperAdmin, s.deleteUser)
	s.registerProjectMemberRoutes()
	s.registerProjectLifecycleRoutes()
	s.registerProjectResourceRoutes()
	s.registerProjectBindingRoutes()
	s.router.GET("/api/v1/projects", privateProjectResponse, s.requireAuthenticated, s.listVisibleProjects)
	domainAPI := s.router.Group("/api/v1", privateProjectResponse, s.requireAuthenticated)
	domainAPI.POST("/domains", s.dnsOperation, s.createDomain)
	domainAPI.PUT("/domains/:id", s.dnsOperation, s.updateDomain)
	domainAPI.DELETE("/domains/:id", s.deleteDomain)
	domainAPI.POST("/domains/:id/issue", s.longOperation(certificateTaskTimeout), s.issueDomain)
	api := s.router.Group("/api/v1", privateProjectResponse, s.requireAuthenticated)
	api.GET("/tasks", s.listTasks)
	api.GET("/tasks/:id", s.getTask)
	api.DELETE("/tasks/:id", s.cancelTask)
	api.POST("/tasks/:id/retry", s.retryTask)
	api.GET("/tasks/:id/attempts", s.listTaskAttempts)

	adminAPI.GET("/settings", s.getSettings)
	adminAPI.PUT("/settings", s.requireSuperAdminSystemConfig, s.updateSettings)
	adminAPI.GET("/dns/providers", s.listAdminDNSProviders)
	adminAPI.GET("/dns/accounts", s.listDNSAccounts)
	adminAPI.POST("/dns/test", s.requireSuperAdminSystemConfig, s.dnsOperation, s.testAdminDNS)
	adminAPI.POST("/dns/aliyun/check", s.requireSuperAdminSystemConfig, s.dnsOperation, s.checkAliyunDNSDomain)
	adminAPI.POST("/renewal/run", s.requireSuperAdmin, s.longOperation(certificateTaskTimeout), s.runRenewalNow)
	domainAPI.POST("/domains/:id/certificate/import", s.longOperation(certificateTaskTimeout), s.importCertificateFiles)
	domainAPI.POST("/domains/:id/certificate/validate", s.longOperation(certificateTaskTimeout), s.validateCertificate)
	domainAPI.POST("/domains/:id/deploy", s.longOperation(certificateTaskTimeout), s.deployCertificateCurrent)
	domainAPI.POST("/domains/:id/certificate/versions/:version/restore", s.longOperation(certificateTaskTimeout), s.restoreCertificateVersion)
	api.POST("/servers", s.createServer)
	api.PUT("/servers/:id", s.updateServer)
	api.DELETE("/servers/:id", s.deleteServer)
	adminAPI.GET("/ssh-keys", s.listSSHKeys)
	adminAPI.POST("/ssh-keys", s.createSSHKey)
	adminAPI.DELETE("/ssh-keys/:id", s.deleteSSHKey)
	s.router.NoRoute(s.frontend)
}

func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) ListenAndServe() error {
	server, err := s.prepareHTTPServer()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	return server.ServeTLS(&servingListener{Listener: listener, ready: s.httpListening}, "", "")
}

// Listening 仅在 TLS 服务进入接收循环后关闭，不代表数据库或代理入口已经就绪。
func (s *Server) Listening() <-chan struct{} {
	return s.httpListening
}

type servingListener struct {
	net.Listener
	once  sync.Once
	ready chan struct{}
}

func (l *servingListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept()
}

func (s *Server) prepareHTTPServer() (*http.Server, error) {
	s.tlsMu.Lock()
	defer s.tlsMu.Unlock()
	s.httpMu.Lock()
	stopped, started := s.httpStopped, s.httpServer != nil
	s.httpMu.Unlock()
	if stopped {
		return nil, http.ErrServerClosed
	}
	if started {
		return nil, errors.New("HTTPS 服务已经启动")
	}
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	if !cfg.SetupReady {
		return nil, errors.New("初始化未完成，请使用独立 HTTPS 初始化服务")
	}
	address, err := listenAddress(cfg)
	if err != nil {
		return nil, err
	}
	certPath := strings.TrimSpace(cfg.Server.TLSCert)
	keyPath := strings.TrimSpace(cfg.Server.TLSKey)
	if certPath == "" || keyPath == "" {
		return nil, fmt.Errorf("初始化已完成，但 HTTPS 证书或私钥路径未配置")
	}
	if _, err := os.Stat(filepath.Clean(certPath)); err != nil {
		return nil, fmt.Errorf("读取 HTTPS 证书失败：%w", err)
	}
	if _, err := os.Stat(filepath.Clean(keyPath)); err != nil {
		return nil, fmt.Errorf("读取 HTTPS 私钥失败：%w", err)
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("加载 HTTPS 证书与私钥失败：%w", err)
	}
	server := newHTTPServer(address, s.router)
	server.TLSConfig.GetCertificate = s.getTLSCertificate
	s.httpMu.Lock()
	if s.httpStopped {
		s.httpMu.Unlock()
		return nil, http.ErrServerClosed
	}
	if s.httpServer != nil {
		s.httpMu.Unlock()
		return nil, errors.New("HTTPS 服务已经启动")
	}
	s.tlsCertificate.Store(&pair)
	s.httpServer = server
	s.httpMu.Unlock()
	if s.logger != nil {
		s.logger.Info("HTTPS 服务启动", "address", server.Addr, "https_mode", cfg.Server.HTTPSMode)
	}
	return server, nil
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

// listenAddress 返回当前 HTTPS 模式实际使用的监听地址。
// 高级模式的核心服务只允许通过本机回环地址提供内部 HTTPS，避免误绑定到公网网卡。
func listenAddress(cfg config.Config) (string, error) {
	host := strings.TrimSpace(cfg.Server.Host)
	if host == "" {
		return "", fmt.Errorf("服务监听地址不能为空")
	}
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return "", fmt.Errorf("服务监听端口无效：%d", cfg.Server.Port)
	}
	if cfg.Server.HTTPSMode == "proxy" && !isLoopbackHost(host) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(cfg.Server.Port)), nil
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.httpMu.Lock()
	s.httpStopped = true
	server := s.httpServer
	s.httpMu.Unlock()
	if server == nil {
		return nil
	}
	err := server.Shutdown(ctx)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type domainRequest struct {
	Name            string   `json:"name" binding:"required"`
	CertificateType string   `json:"certificate_type" binding:"required"`
	SANs            []string `json:"sans"`
	AutoRenew       *bool    `json:"auto_renew"`
	ACMEEnvironment string   `json:"acme_environment"`
	DNSProvider     string   `json:"dns_provider"`
}

type serverRequest struct {
	Name                 string   `json:"name" binding:"required"`
	Address              string   `json:"address" binding:"required"`
	Port                 int      `json:"port"`
	Username             string   `json:"username" binding:"required"`
	AuthMethod           string   `json:"auth_method"`
	PrivateKeyRef        string   `json:"private_key_ref"`
	Password             string   `json:"password"`
	HostKeyFingerprint   string   `json:"host_key_fingerprint" binding:"required"`
	CertificatePath      string   `json:"certificate_path"`
	PrivateKeyPath       string   `json:"private_key_path"`
	CertificateChainPath string   `json:"certificate_chain_path"`
	WebServer            string   `json:"web_server" binding:"required"`
	ValidationCommand    []string `json:"validation_command"`
	ReloadCommand        []string `json:"reload_command"`
	ProjectID            string   `json:"project_id"`
}

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "scert"})
}

func (s *Server) ready(c *gin.Context) {
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	if !cfg.SetupReady {
		s.writeReadinessFailure(c, "setup_incomplete")
		return
	}

	dnsReference := strings.TrimSpace(cfg.DNS.CredentialRef)
	if dnsReference == "" {
		s.writeReadinessFailure(c, "dns_credential_missing")
		return
	}
	references := []string{
		dnsReference,
		strings.TrimSpace(cfg.Notification.HTTPCredentialRef),
		strings.TrimSpace(cfg.Notification.SMTPCredentialRef),
	}
	reader, ok := s.secrets.(readinessSecretReader)
	if !ok {
		s.writeReadinessFailure(c, "secret_store_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	checked := make(map[string]struct{}, len(references))
	for _, reference := range references {
		if reference == "" {
			continue
		}
		if _, exists := checked[reference]; exists {
			continue
		}
		checked[reference] = struct{}{}
		credentials, err := reader.Get(ctx, reference)
		if err != nil {
			s.writeReadinessFailure(c, "credential_unavailable")
			return
		}
		if reference == dnsReference {
			if _, err := dnsprovider.ValidateCredentials(cfg.DNS.Provider, credentials); err != nil {
				s.writeReadinessFailure(c, "dns_credential_invalid")
				return
			}
		}
	}

	for _, path := range []string{cfg.Server.TLSCert, cfg.Server.TLSKey} {
		info, err := os.Lstat(filepath.Clean(strings.TrimSpace(path)))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			s.writeReadinessFailure(c, "tls_material_unavailable")
			return
		}
	}
	if s.readinessCheck != nil {
		if err := s.readinessCheck(ctx); err != nil {
			s.writeReadinessFailure(c, "database_unavailable")
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready", "service": "scert"})
}

func (s *Server) writeReadinessFailure(c *gin.Context, reason string) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"status":  "not_ready",
		"service": "scert",
		"reason":  reason,
	})
}

func (s *Server) resetPage(c *gin.Context) {
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	session, ok := s.currentResetSession()
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	token, err := newCSRFToken()
	if err != nil {
		s.writeResetFailure(c, http.StatusInternalServerError, "暂时无法生成安全校验信息，请稍后重试")
		return
	}
	s.setResetCSRFCookie(c, token, session.ExpiresAt)
	s.renderResetPage(c, http.StatusOK, token, "")
}

func (s *Server) status(c *gin.Context) {
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	payload := gin.H{
		"service":        "scert",
		"version":        buildinfo.Version,
		"config_version": cfg.Version,
		"setup_ready":    cfg.SetupReady,
	}
	if _, authenticated := s.currentAuthSession(c); authenticated {
		payload["https_mode"] = cfg.Server.HTTPSMode
		payload["database_driver"] = cfg.Database.Driver
		payload["reset_method"] = "local_admin_command"
	}
	c.JSON(http.StatusOK, payload)
}

func (s *Server) requireDomainRepository(c *gin.Context) bool {
	if s.repository != nil {
		return true
	}
	s.writeError(c, http.StatusNotImplemented, "域名管理接口尚未配置", errors.New("域名仓储未配置"))
	return false
}

func (s *Server) requireProjectDomainRepository(c *gin.Context) bool {
	if s.projectDomains != nil {
		return true
	}
	s.writeError(c, http.StatusServiceUnavailable, "项目域名服务尚未就绪", nil)
	return false
}

func (s *Server) authorizeDomainMutation(c *gin.Context, projectID string) bool {
	authorizer, ok := s.projectDomains.(repository.ProjectAuthorizer)
	if !ok {
		s.writeError(c, http.StatusServiceUnavailable, "项目权限服务尚未就绪", nil)
		return false
	}
	if err := authorizer.AuthorizeProject(c.Request.Context(), c.GetString(authenticatedActorKey), projectID, domain.PermissionCertificateManage); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权管理此项目的域名", nil)
		} else {
			s.writeError(c, http.StatusInternalServerError, "检查项目域名权限失败", err)
		}
		return false
	}
	return true
}

func (s *Server) authorizeServerMutation(c *gin.Context, projectID string) bool {
	authorizer, ok := s.projectDomains.(repository.ProjectAuthorizer)
	if !ok {
		if fallback, fallbackOK := s.projectMembers.(repository.ProjectAuthorizer); fallbackOK {
			authorizer = fallback
			ok = true
		}
	}
	if !ok {
		s.writeError(c, http.StatusServiceUnavailable, "项目权限服务尚未就绪", nil)
		return false
	}
	if err := authorizer.AuthorizeProject(c.Request.Context(), c.GetString(authenticatedActorKey), projectID, domain.PermissionServerManage); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权管理此项目的服务器", nil)
		} else {
			s.writeError(c, http.StatusInternalServerError, "检查项目服务器权限失败", err)
		}
		return false
	}
	return true
}

func (s *Server) currentDNSProvider() string {
	s.configMu.RLock()
	provider := strings.ToLower(strings.TrimSpace(s.config.DNS.Provider))
	s.configMu.RUnlock()
	return provider
}

func (s *Server) listDomains(c *gin.Context) {
	if !s.requireProjectResourceReader(c) {
		return
	}
	items, err := s.projectResources.ListVisibleDomains(c.Request.Context(), c.GetString(authenticatedActorKey))
	if err != nil {
		s.writeProjectResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) createDomain(c *gin.Context) {
	if !s.requireProjectDomainRepository(c) {
		return
	}
	var request domainRequest
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "请求参数无效", err)
		return
	}
	item, err := newDomainFromRequest(request)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "域名配置无效", err)
		return
	}
	item.ResourceScope = s.newResourceScope(c)
	if !s.authorizeDomainMutation(c, item.ProjectID) {
		return
	}
	if s.currentDNSProvider() == dnsprovider.Aliyun && !s.authorizeDNSCredentialUse(c, item.ProjectID) {
		return
	}
	if err := s.validateDomainAliyunAuthorization(c.Request.Context(), append([]string{item.Name}, item.SANs...)...); err != nil {
		s.writeError(c, http.StatusBadRequest, err.Error(), err)
		return
	}
	if err := s.projectDomains.CreateProjectDomain(c.Request.Context(), c.GetString(authenticatedActorKey), item); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权管理此项目的域名", nil)
			return
		}
		if repository.IsConflict(err) {
			s.writeError(c, http.StatusConflict, "域名已经存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "保存域名失败", err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (s *Server) updateDomain(c *gin.Context) {
	if !s.requireProjectDomainRepository(c) || !s.requireProjectResourceReader(c) {
		return
	}
	var request domainRequest
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "请求参数无效", err)
		return
	}
	existing, err := s.projectResources.GetVisibleDomain(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		s.writeProjectResourceError(c, err)
		return
	}
	if !s.authorizeDomainMutation(c, existing.ProjectID) {
		return
	}
	if s.currentDNSProvider() == dnsprovider.Aliyun && !s.authorizeDNSCredentialUse(c, existing.ProjectID) {
		return
	}
	item, err := newDomainFromRequest(request)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "域名配置无效", err)
		return
	}
	if err := s.validateDomainAliyunAuthorization(c.Request.Context(), append([]string{item.Name}, item.SANs...)...); err != nil {
		s.writeError(c, http.StatusBadRequest, err.Error(), err)
		return
	}
	item.ID = existing.ID
	item.ResourceScope = s.updatedResourceScope(c, existing.ResourceScope)
	item.CreatedAt = existing.CreatedAt
	item.Status = "pending"
	item.ExpiresAt = nil
	if err := s.projectDomains.UpdateProjectDomain(c.Request.Context(), c.GetString(authenticatedActorKey), item); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权管理此项目的域名", nil)
			return
		}
		if errors.Is(err, deployment.ErrSharedPathConflict) {
			s.writeError(c, http.StatusConflict, deployment.ErrSharedPathConflict.Error(), nil)
			return
		}
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "域名不存在", err)
			return
		}
		if repository.IsConflict(err) {
			s.writeError(c, http.StatusConflict, "域名已经存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "更新域名失败", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func newDomainFromRequest(request domainRequest) (domain.Domain, error) {
	autoRenew := true
	if request.AutoRenew != nil {
		autoRenew = *request.AutoRenew
	}
	acmeEnvironment := request.ACMEEnvironment
	if acmeEnvironment == "" {
		acmeEnvironment = domain.ACMEEnvironmentStaging
	}
	return domain.NewDomainWithOptions(request.Name, domain.CertificateType(request.CertificateType), request.SANs, autoRenew, acmeEnvironment, request.DNSProvider)
}

func (s *Server) deleteDomain(c *gin.Context) {
	if !s.requireProjectDomainRepository(c) {
		return
	}
	domainID := strings.TrimSpace(c.Param("id"))
	if err := s.projectDomains.DeleteProjectDomain(c.Request.Context(), c.GetString(authenticatedActorKey), domainID); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权管理此项目的域名", nil)
			return
		}
		if errors.Is(err, repository.ErrProjectDomainBusy) {
			s.writeError(c, http.StatusConflict, err.Error(), nil)
			return
		}
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "域名不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "删除域名失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) authorizeSynchronousCertificateIssue(c *gin.Context) bool {
	if !s.authorizeVisibleDomainPermissions(c, domain.PermissionCertificateManage, domain.PermissionDeploymentExecute) {
		return false
	}
	resource, err := s.projectResources.GetVisibleDomain(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		s.writeProjectResourceError(c, err)
		return false
	}
	return s.authorizeDNSCredentialUse(c, resource.ResourceScope.Normalize().ProjectID)
}

func (s *Server) issueDomain(c *gin.Context) {
	if s.tasks != nil {
		s.issueDomainAsTask(c)
		return
	}
	if !s.authorizeSynchronousCertificateIssue(c) {
		return
	}
	if s.issueCertificate == nil {
		s.writeError(c, http.StatusNotImplemented, "证书申请接口尚未配置", errors.New("证书申请工作流未配置"))
		return
	}
	certificateResult, deploymentReport, err := s.issueCertificate(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.writeError(c, http.StatusBadGateway, "证书申请或部署失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"certificate": certificateResult, "deployment": sanitizeDeploymentReport(deploymentReport)})
}

func (s *Server) listServers(c *gin.Context) {
	if !s.requireProjectResourceReader(c) {
		return
	}
	items, err := s.projectResources.ListVisibleServers(c.Request.Context(), c.GetString(authenticatedActorKey))
	if err != nil {
		s.writeProjectResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) getServer(c *gin.Context) {
	if !s.requireProjectResourceReader(c) {
		return
	}
	item, err := s.projectResources.GetVisibleServer(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		s.writeProjectResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) createServer(c *gin.Context) {
	if s.servers == nil {
		s.writeError(c, http.StatusNotImplemented, "服务器管理接口尚未配置", errors.New("服务器仓储未配置"))
		return
	}
	var request serverRequest
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "请求参数无效", err)
		return
	}
	item := serverFromRequest(request)
	item.ResourceScope = s.newResourceScope(c)
	if strings.TrimSpace(request.ProjectID) != "" {
		item.ProjectID = strings.TrimSpace(request.ProjectID)
	}
	item.ResourceScope = item.ResourceScope.Normalize()
	if !s.authorizeServerMutation(c, item.ProjectID) {
		return
	}
	item = item.Normalize()
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	item, cleanup, err := s.prepareServerCredential(c.Request.Context(), item, request.Password, nil)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "SSH 认证信息无效", err)
		return
	}
	if err := s.servers.CreateServer(c.Request.Context(), item); err != nil {
		cleanup()
		s.writeError(c, serverErrorStatus(err), "保存服务器失败", err)
		return
	}
	s.recordServerHostKeyAudit(c, "server_host_key_fingerprint_register", item.ID)
	item, provisionErr := s.provisionServerSSHKey(c.Request.Context(), item)
	if provisionErr != nil {
		if updateErr := s.servers.UpdateServer(c.Request.Context(), item); updateErr != nil && s.logger != nil {
			s.logger.Warn("保存 SSH 密钥部署失败状态失败", "error", updateErr)
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": item.SSHKeyError, "server": item})
		return
	}
	if err := s.servers.UpdateServer(c.Request.Context(), item); err != nil {
		s.writeError(c, http.StatusInternalServerError, "保存 SSH 密钥部署状态失败", err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (s *Server) updateServer(c *gin.Context) {
	if s.servers == nil {
		s.writeError(c, http.StatusNotImplemented, "服务器管理接口尚未配置", errors.New("服务器仓储未配置"))
		return
	}
	if !s.requireProjectResourceReader(c) {
		return
	}
	var request serverRequest
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "请求参数无效", err)
		return
	}
	item := serverFromRequest(request)
	item.ID = c.Param("id")
	item = item.Normalize()
	existing, err := s.projectResources.GetVisibleServer(c.Request.Context(), c.GetString(authenticatedActorKey), item.ID)
	if err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeProjectResourceError(c, err)
			return
		}
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "服务器不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取服务器失败", err)
		return
	}
	if !s.authorizeServerMutation(c, existing.ProjectID) {
		return
	}
	item.CreatedAt = existing.CreatedAt
	item.ResourceScope = s.updatedResourceScope(c, existing.ResourceScope)
	item.UpdatedAt = time.Now().UTC()
	item, cleanup, err := s.prepareServerCredential(c.Request.Context(), item, request.Password, &existing)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "SSH 认证信息无效", err)
		return
	}
	if err := s.servers.UpdateServer(c.Request.Context(), item); err != nil {
		cleanup()
		if errors.Is(err, deployment.ErrSharedPathConflict) {
			s.writeError(c, http.StatusConflict, deployment.ErrSharedPathConflict.Error(), nil)
			return
		}
		s.writeError(c, serverErrorStatus(err), "更新服务器失败", err)
		return
	}
	if existing.HostKeyFingerprint != item.HostKeyFingerprint {
		s.recordServerHostKeyAudit(c, "server_host_key_fingerprint_update", item.ID)
	}
	item, provisionErr := s.provisionServerSSHKey(c.Request.Context(), item)
	if provisionErr != nil {
		if updateErr := s.servers.UpdateServer(c.Request.Context(), item); updateErr != nil && s.logger != nil {
			s.logger.Warn("保存 SSH 密钥部署失败状态失败", "error", updateErr)
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": item.SSHKeyError, "server": item})
		return
	}
	if err := s.servers.UpdateServer(c.Request.Context(), item); err != nil {
		s.writeError(c, http.StatusInternalServerError, "保存 SSH 密钥部署状态失败", err)
		return
	}
	if existing.PasswordRef != "" && existing.PasswordRef != item.PasswordRef && s.secrets != nil {
		if err := s.secrets.Delete(c.Request.Context(), existing.PasswordRef); err != nil && s.logger != nil {
			s.logger.Warn("删除旧 SSH 密码失败", "error", err)
		}
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) validateSSHKeyReference(ctx context.Context, reference, projectID string) error {
	if s.sshKeys == nil {
		return errors.New("SSH 私钥管理服务未配置")
	}
	_, err := s.sshKeys.ValidateReferenceForProject(ctx, reference, projectID)
	return err
}

func (s *Server) prepareServerCredential(ctx context.Context, item deployment.Server, password string, existing *deployment.Server) (deployment.Server, func(), error) {
	item = item.Normalize()
	cleanup := func() {}
	if item.AuthMethod == deployment.SSHAuthPrivateKey {
		if err := s.validateSSHKeyReference(ctx, item.PrivateKeyRef, item.ProjectID); err != nil {
			return deployment.Server{}, cleanup, err
		}
		if existing != nil && item.PasswordRef == "" {
			item.PasswordRef = existing.PasswordRef
		}
		return item, cleanup, nil
	}
	if item.AuthMethod != deployment.SSHAuthPassword {
		return deployment.Server{}, cleanup, errors.New("SSH \u8ba4\u8bc1\u65b9\u5f0f\u65e0\u6548")
	}
	if s.secrets == nil {
		return deployment.Server{}, cleanup, errors.New("SSH \u5bc6\u7801\u5b58\u50a8\u672a\u521d\u59cb\u5316")
	}
	if password == "" && existing != nil && existing.PasswordRef != "" {
		item.PasswordRef = existing.PasswordRef
	}
	if item.PasswordRef == "" && password == "" {
		return deployment.Server{}, cleanup, errors.New("SSH \u5bc6\u7801\u4e0d\u80fd\u4e3a\u7a7a")
	}
	if item.PasswordRef == "" {
		item.PasswordRef = "secret://ssh/password/" + domain.NewID()
		if err := s.secrets.Put(ctx, item.PasswordRef, map[string]string{"kind": "ssh_password", "password": password}); err != nil {
			return deployment.Server{}, cleanup, fmt.Errorf("\u4fdd\u5b58 SSH \u5bc6\u7801\u5931\u8d25\uff1a%w", err)
		}
		cleanup = func() {
			_ = s.secrets.Delete(context.Background(), item.PasswordRef)
		}
	}
	if item.PrivateKeyRef == "" && s.sshKeys != nil && s.deploymentFactory != nil {
		metadata, _, err := s.sshKeys.CreateGeneratedForProject(ctx, "服务器 "+item.Name+" SSH密钥", item.ProjectID)
		if err != nil {
			return deployment.Server{}, cleanup, fmt.Errorf("生成 SSH 密钥失败：%w", err)
		}
		item.PrivateKeyRef = metadata.Reference
		item.SSHKeyFingerprint = metadata.Fingerprint
		item.SSHKeyStatus = deployment.SSHKeyStatusPending
	}
	return item, cleanup, nil
}

func (s *Server) provisionServerSSHKey(ctx context.Context, item deployment.Server) (deployment.Server, error) {
	item = item.Normalize()
	if item.AuthMethod != deployment.SSHAuthPassword || item.PrivateKeyRef == "" || s.sshKeys == nil || s.deploymentFactory == nil {
		return item, nil
	}
	publicKey, err := s.sshKeys.AuthorizedPublicKey(ctx, item.PrivateKeyRef)
	if err != nil {
		item.SSHKeyStatus = deployment.SSHKeyStatusFailed
		item.SSHKeyError = "自动部署 SSH 公钥失败"
		return item, err
	}
	item.SSHKeyStatus = deployment.SSHKeyStatusPending
	item.SSHKeyError = ""
	passwordClient, err := s.connectSSHForKey(ctx, item)
	if err != nil {
		s.logSSHProvisionFailure("password_connect", item, err)
		item.SSHKeyStatus = deployment.SSHKeyStatusFailed
		item.SSHKeyError = "自动部署 SSH 公钥失败"
		return item, err
	}
	if err := passwordClient.InstallAuthorizedKey(ctx, publicKey); err != nil {
		s.logSSHProvisionFailure("authorized_key_install", item, err)
		_ = passwordClient.Close()
		item.SSHKeyStatus = deployment.SSHKeyStatusFailed
		item.SSHKeyError = "自动部署 SSH 公钥失败"
		return item, err
	}
	if err := passwordClient.Close(); err != nil {
		item.SSHKeyStatus = deployment.SSHKeyStatusFailed
		item.SSHKeyError = "自动部署 SSH 公钥失败"
		return item, err
	}
	privateItem := item
	privateItem.AuthMethod = deployment.SSHAuthPrivateKey
	privateClient, err := s.connectSSHForKey(ctx, privateItem)
	if err != nil {
		s.logSSHProvisionFailure("private_key_connect", item, err)
		item.SSHKeyStatus = deployment.SSHKeyStatusFailed
		item.SSHKeyError = "自动验证 SSH 密钥登录失败"
		return item, err
	}
	defer privateClient.Close()
	result, err := privateClient.Run(ctx, []string{"true"})
	if err != nil {
		s.logSSHProvisionFailure("private_key_command", item, err)
		item.SSHKeyStatus = deployment.SSHKeyStatusFailed
		item.SSHKeyError = "自动验证 SSH 密钥登录失败"
		return item, err
	}
	if result.ExitCode != 0 {
		s.logSSHProvisionFailure("private_key_exit", item, fmt.Errorf("远程退出码：%d", result.ExitCode))
		item.SSHKeyStatus = deployment.SSHKeyStatusFailed
		item.SSHKeyError = "自动验证 SSH 密钥登录失败"
		return item, fmt.Errorf("远程验证退出码：%d", result.ExitCode)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	item.AuthMethod = deployment.SSHAuthPrivateKey
	item.SSHKeyStatus = deployment.SSHKeyStatusDeployed
	item.SSHKeyDeployedAt = now
	item.SSHKeyTestedAt = now
	item.SSHKeyError = ""
	return item, nil
}

func (s *Server) connectSSHForKey(ctx context.Context, item deployment.Server) (deployment.Client, error) {
	if factory, ok := s.deploymentFactory.(deployment.ShellClientFactory); ok {
		return factory.ConnectShell(ctx, item)
	}
	return s.deploymentFactory.Connect(ctx, item)
}

func (s *Server) logSSHProvisionFailure(stage string, item deployment.Server, err error) {
	if s.logger == nil {
		return
	}
	s.logger.Warn("服务器 SSH 密钥流程失败", "stage", stage, "server_id", item.ID, "server_name", item.Name, "error", err)
}
func (s *Server) deleteServer(c *gin.Context) {
	if s.servers == nil {
		s.writeError(c, http.StatusNotImplemented, "服务器管理接口尚未配置", errors.New("服务器仓储未配置"))
		return
	}
	if !s.requireProjectResourceReader(c) {
		return
	}
	item, err := s.projectResources.GetVisibleServer(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeProjectResourceError(c, err)
			return
		}
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "服务器不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取服务器失败", err)
		return
	}
	if !s.authorizeServerMutation(c, item.ProjectID) {
		return
	}
	if err := s.servers.DeleteServer(c.Request.Context(), c.Param("id")); err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "服务器不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "删除服务器失败", err)
		return
	}
	if item.PasswordRef != "" && s.secrets != nil {
		if err := s.secrets.Delete(c.Request.Context(), item.PasswordRef); err != nil && s.logger != nil {
			s.logger.Warn("删除 SSH 密码失败", "error", err)
		}
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) listDomainServers(c *gin.Context) {
	if !s.requireProjectResourceReader(c) {
		return
	}
	items, err := s.projectResources.ListVisibleDomainServers(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		s.writeProjectResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) listServerDomains(c *gin.Context) {
	if !s.requireProjectResourceReader(c) {
		return
	}
	items, err := s.projectResources.ListVisibleServerDomains(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		s.writeProjectResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func serverFromRequest(request serverRequest) deployment.Server {
	return deployment.Server{
		ID: domain.NewID(), Name: request.Name, Address: request.Address, Port: request.Port,
		Username: request.Username, AuthMethod: deployment.SSHAuthMethod(request.AuthMethod), PrivateKeyRef: request.PrivateKeyRef, HostKeyFingerprint: request.HostKeyFingerprint,
		CertificatePath: request.CertificatePath, PrivateKeyPath: request.PrivateKeyPath,
		CertificateChainPath: request.CertificateChainPath, WebServer: deployment.WebServer(request.WebServer),
		ValidationCommand: request.ValidationCommand, ReloadCommand: request.ReloadCommand,
	}
}

func serverErrorStatus(err error) int {
	if errors.Is(err, deployment.ErrSharedPathConflict) {
		return http.StatusConflict
	}
	if repository.IsConflict(err) {
		return http.StatusConflict
	}
	if repository.IsNotFound(err) {
		return http.StatusNotFound
	}
	if strings.Contains(err.Error(), "不存在") || strings.Contains(err.Error(), "不能为空") || strings.Contains(err.Error(), "无效") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func (s *Server) writeError(c *gin.Context, status int, message string, err error) {
	if s.logger != nil {
		s.logger.Error(message, "error", err)
	}
	c.JSON(status, gin.H{"error": message})
}

func (s *Server) recordServerHostKeyAudit(c *gin.Context, action, serverID string) {
	if s.audit == nil {
		return
	}
	if err := s.audit.CreateAuditLog(c.Request.Context(), repository.AuditLog{
		OperatorID: s.currentOperatorID(c),
		Action:     action, ResourceType: "server", ResourceID: serverID, Status: "success",
		Message: "已登记服务器主机指纹变更",
	}); err != nil && s.logger != nil {
		s.logger.Warn("保存服务器主机指纹审计日志失败", "server_id", serverID, "error", err)
	}
}
