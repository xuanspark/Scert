package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
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
	SSHKeys                   *sshkey.Service
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
	sshKeys                  *sshkey.Service
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
		sshKeys:                  deps.SSHKeys,
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
	adminAPI.GET("/domains", s.listDomains)
	adminAPI.POST("/domains", s.dnsOperation, s.createDomain)
	adminAPI.PUT("/domains/:id", s.dnsOperation, s.updateDomain)
	adminAPI.DELETE("/domains/:id", s.deleteDomain)
	adminAPI.POST("/domains/:id/issue", s.longOperation(certificateTaskTimeout), s.issueDomain)
	adminAPI.GET("/tasks", s.listTasks)
	adminAPI.GET("/tasks/:id", s.getTask)
	adminAPI.DELETE("/tasks/:id", s.cancelTask)
	adminAPI.GET("/tasks/:id/attempts", s.listTaskAttempts)
	adminAPI.GET("/audit-logs", s.listAuditLogs)
	adminAPI.GET("/settings", s.getSettings)
	adminAPI.PUT("/settings", s.updateSettings)
	adminAPI.GET("/dns/providers", s.listAdminDNSProviders)
	adminAPI.POST("/dns/test", s.dnsOperation, s.testAdminDNS)
	adminAPI.POST("/dns/aliyun/check", s.dnsOperation, s.checkAliyunDNSDomain)
	adminAPI.POST("/renewal/run", s.longOperation(certificateTaskTimeout), s.runRenewalNow)
	adminAPI.GET("/certificates", s.listCertificates)
	adminAPI.GET("/certificates/:id", s.getCertificate)
	adminAPI.POST("/domains/:id/certificate/import", s.longOperation(certificateTaskTimeout), s.importCertificateFiles)
	adminAPI.POST("/domains/:id/certificate/validate", s.longOperation(certificateTaskTimeout), s.validateCertificate)
	adminAPI.POST("/domains/:id/deploy", s.longOperation(certificateTaskTimeout), s.deployCertificateCurrent)
	adminAPI.GET("/domains/:id/certificate/versions", s.listCertificateVersions)
	adminAPI.POST("/domains/:id/certificate/versions/:version/restore", s.longOperation(certificateTaskTimeout), s.restoreCertificateVersion)
	adminAPI.GET("/domains/:id/servers", s.listDomainServers)
	adminAPI.PUT("/domains/:id/servers", s.replaceDomainServers)
	adminAPI.GET("/servers", s.listServers)
	adminAPI.POST("/servers", s.createServer)
	adminAPI.GET("/servers/:id", s.getServer)
	adminAPI.PUT("/servers/:id", s.updateServer)
	adminAPI.DELETE("/servers/:id", s.deleteServer)
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
}

type domainServersRequest struct {
	ServerIDs []string `json:"server_ids"`
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

func (s *Server) listDomains(c *gin.Context) {
	if !s.requireDomainRepository(c) {
		return
	}
	items, err := s.repository.List(c.Request.Context())
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询域名失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) createDomain(c *gin.Context) {
	if !s.requireDomainRepository(c) {
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
	if err := s.validateDomainAliyunAuthorization(c.Request.Context(), append([]string{item.Name}, item.SANs...)...); err != nil {
		s.writeError(c, http.StatusBadRequest, err.Error(), err)
		return
	}
	if err := s.repository.Create(c.Request.Context(), item); err != nil {
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
	if !s.requireDomainRepository(c) {
		return
	}
	var request domainRequest
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "请求参数无效", err)
		return
	}
	existing, err := s.repository.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "域名不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取域名失败", err)
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
	item.CreatedAt = existing.CreatedAt
	item.Status = "pending"
	item.ExpiresAt = nil
	if err := s.repository.Update(c.Request.Context(), item); err != nil {
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
	if !s.requireDomainRepository(c) {
		return
	}
	domainID := strings.TrimSpace(c.Param("id"))
	if s.tasks != nil {
		for _, taskType := range []string{"certificate_issue", "certificate_validate", "certificate_deploy", "certificate_import", "certificate_restore"} {
			_, err := s.tasks.FindActiveTaskByDomain(c.Request.Context(), domainID, taskType)
			if err == nil {
				s.writeError(c, http.StatusConflict, "域名存在正在执行的证书任务，请等待任务完成或取消后再删除", nil)
				return
			}
			if !errors.Is(err, sql.ErrNoRows) {
				s.writeError(c, http.StatusInternalServerError, "检查域名任务状态失败", err)
				return
			}
		}
	}
	if err := s.repository.Delete(c.Request.Context(), domainID); err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "域名不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "删除域名失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) issueDomain(c *gin.Context) {
	if s.tasks != nil {
		s.issueDomainAsTask(c)
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
	if s.servers == nil {
		s.writeError(c, http.StatusNotImplemented, "服务器管理接口尚未配置", errors.New("服务器仓储未配置"))
		return
	}
	items, err := s.servers.ListServers(c.Request.Context())
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询服务器失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) getServer(c *gin.Context) {
	if s.servers == nil {
		s.writeError(c, http.StatusNotImplemented, "服务器管理接口尚未配置", errors.New("服务器仓储未配置"))
		return
	}
	item, err := s.servers.GetServerByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "服务器不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取服务器失败", err)
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
	c.JSON(http.StatusCreated, item)
}

func (s *Server) updateServer(c *gin.Context) {
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
	item.ID = c.Param("id")
	item = item.Normalize()
	existing, err := s.servers.GetServerByID(c.Request.Context(), item.ID)
	if err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "服务器不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取服务器失败", err)
		return
	}
	item.CreatedAt = existing.CreatedAt
	item.UpdatedAt = time.Now().UTC()
	item, cleanup, err := s.prepareServerCredential(c.Request.Context(), item, request.Password, &existing)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "SSH 认证信息无效", err)
		return
	}
	if err := s.servers.UpdateServer(c.Request.Context(), item); err != nil {
		cleanup()
		s.writeError(c, serverErrorStatus(err), "更新服务器失败", err)
		return
	}
	if existing.PasswordRef != "" && existing.PasswordRef != item.PasswordRef && s.secrets != nil {
		if err := s.secrets.Delete(c.Request.Context(), existing.PasswordRef); err != nil && s.logger != nil {
			s.logger.Warn("删除旧 SSH 密码失败", "error", err)
		}
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) validateSSHKeyReference(ctx context.Context, reference string) error {
	if s.sshKeys == nil {
		return errors.New("SSH 私钥管理服务未配置")
	}
	_, err := s.sshKeys.ValidateReference(ctx, reference)
	return err
}

func (s *Server) prepareServerCredential(ctx context.Context, item deployment.Server, password string, existing *deployment.Server) (deployment.Server, func(), error) {
	item = item.Normalize()
	cleanup := func() {}
	if item.AuthMethod == deployment.SSHAuthPrivateKey {
		if err := s.validateSSHKeyReference(ctx, item.PrivateKeyRef); err != nil {
			return deployment.Server{}, cleanup, err
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
		return item, cleanup, nil
	}
	if password == "" {
		return deployment.Server{}, cleanup, errors.New("SSH \u5bc6\u7801\u4e0d\u80fd\u4e3a\u7a7a")
	}
	item.PasswordRef = "secret://ssh/password/" + domain.NewID()
	if err := s.secrets.Put(ctx, item.PasswordRef, map[string]string{"kind": "ssh_password", "password": password}); err != nil {
		return deployment.Server{}, cleanup, fmt.Errorf("\u4fdd\u5b58 SSH \u5bc6\u7801\u5931\u8d25\uff1a%w", err)
	}
	cleanup = func() {
		_ = s.secrets.Delete(context.Background(), item.PasswordRef)
	}
	return item, cleanup, nil
}

func (s *Server) deleteServer(c *gin.Context) {
	if s.servers == nil {
		s.writeError(c, http.StatusNotImplemented, "服务器管理接口尚未配置", errors.New("服务器仓储未配置"))
		return
	}
	item, err := s.servers.GetServerByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "服务器不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取服务器失败", err)
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
	if !s.requireDomainRepository(c) {
		return
	}
	if s.servers == nil {
		s.writeError(c, http.StatusNotImplemented, "服务器管理接口尚未配置", errors.New("服务器仓储未配置"))
		return
	}
	if _, err := s.repository.GetByID(c.Request.Context(), c.Param("id")); err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "域名不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取域名失败", err)
		return
	}
	items, err := s.servers.ListServersByDomainID(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询域名部署服务器失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) replaceDomainServers(c *gin.Context) {
	if !s.requireDomainRepository(c) {
		return
	}
	if s.servers == nil {
		s.writeError(c, http.StatusNotImplemented, "服务器管理接口尚未配置", errors.New("服务器仓储未配置"))
		return
	}
	if _, err := s.repository.GetByID(c.Request.Context(), c.Param("id")); err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "域名不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取域名失败", err)
		return
	}
	var request domainServersRequest
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "请求参数无效", err)
		return
	}
	if err := s.servers.ReplaceDomainServers(c.Request.Context(), c.Param("id"), request.ServerIDs); err != nil {
		s.writeError(c, serverErrorStatus(err), "保存域名部署服务器失败", err)
		return
	}
	items, err := s.servers.ListServersByDomainID(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "读取域名部署服务器失败", err)
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
