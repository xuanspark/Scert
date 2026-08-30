package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/admin"
	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
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
	taskMu              sync.Mutex
	taskCancels         map[string]context.CancelFunc
	issueTaskMu         sync.Mutex
	resetMu             sync.Mutex
	resetClosedHash     string
	resetLimiter        *resetRateLimiter
	authSessions        map[string]authSession
	authMu              sync.RWMutex
	loginAttempts       map[string]loginAttempt
	loginMu             sync.Mutex
	now                 func() time.Time
	httpServer          *http.Server
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
		config: deps.Config, configPath: deps.ConfigPath, frontendDir: filepath.Clean(frontendDir), logger: deps.Logger,
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
		taskCancels:              make(map[string]context.CancelFunc),
		resetLimiter:             newResetRateLimiter(),
		authSessions:             make(map[string]authSession),
		loginAttempts:            make(map[string]loginAttempt),
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
	s.router.GET(admin.ResetPath, s.resetPage)
	s.router.POST(admin.ResetPath, s.resetSubmit)
	s.router.GET("/api/v1/status", s.status)
	s.router.GET("/api/v1/auth/session", s.authSession)
	s.router.POST("/api/v1/auth/login", s.authLogin)
	s.router.POST("/api/v1/auth/logout", s.authLogout)
	s.router.POST("/api/v1/auth/password", s.requireAuthenticated, s.changePassword)
	s.router.POST("/api/v1/password-reset/resolve", s.resolvePasswordResetAccount)
	adminAPI := s.router.Group("/api/v1", s.requireAdmin)
	userAPI := s.router.Group("/api/v1/users", s.requireSuperAdmin)
	userAPI.GET("", s.listUsers)
	userAPI.POST("", s.createUser)
	userAPI.GET("/:id", s.getUser)
	userAPI.PUT("/:id", s.updateUser)
	userAPI.DELETE("/:id", s.deleteUser)
	adminAPI.GET("/domains", s.listDomains)
	adminAPI.POST("/domains", s.createDomain)
	adminAPI.PUT("/domains/:id", s.updateDomain)
	adminAPI.DELETE("/domains/:id", s.deleteDomain)
	adminAPI.POST("/domains/:id/issue", s.issueDomain)
	adminAPI.GET("/tasks", s.listTasks)
	adminAPI.GET("/tasks/:id", s.getTask)
	adminAPI.DELETE("/tasks/:id", s.cancelTask)
	adminAPI.GET("/tasks/:id/attempts", s.listTaskAttempts)
	adminAPI.GET("/audit-logs", s.listAuditLogs)
	adminAPI.GET("/settings", s.getSettings)
	adminAPI.PUT("/settings", s.updateSettings)
	adminAPI.GET("/dns/providers", s.listAdminDNSProviders)
	adminAPI.POST("/dns/test", s.testAdminDNS)
	adminAPI.POST("/renewal/run", s.runRenewalNow)
	adminAPI.GET("/certificates", s.listCertificates)
	adminAPI.GET("/certificates/:id", s.getCertificate)
	adminAPI.POST("/domains/:id/certificate/import", s.importCertificateFiles)
	adminAPI.POST("/domains/:id/certificate/validate", s.validateCertificate)
	adminAPI.POST("/domains/:id/deploy", s.deployCertificateCurrent)
	adminAPI.GET("/domains/:id/certificate/versions", s.listCertificateVersions)
	adminAPI.POST("/domains/:id/certificate/versions/:version/restore", s.restoreCertificateVersion)
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
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	address, err := listenAddress(cfg)
	if err != nil {
		return err
	}
	s.httpServer = &http.Server{
		Addr:              address,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if !cfg.SetupReady {
		if s.logger != nil {
			s.logger.Info("HTTP 服务启动", "address", s.httpServer.Addr, "https_mode", "初始化明文 HTTP")
		}
		return s.httpServer.ListenAndServe()
	}
	certPath := strings.TrimSpace(cfg.Server.TLSCert)
	keyPath := strings.TrimSpace(cfg.Server.TLSKey)
	if certPath == "" || keyPath == "" {
		return fmt.Errorf("初始化已完成，但 HTTPS 证书或私钥路径未配置")
	}
	if _, err := os.Stat(filepath.Clean(certPath)); err != nil {
		return fmt.Errorf("读取 HTTPS 证书失败：%w", err)
	}
	if _, err := os.Stat(filepath.Clean(keyPath)); err != nil {
		return fmt.Errorf("读取 HTTPS 私钥失败：%w", err)
	}
	if s.logger != nil {
		s.logger.Info("HTTPS 服务启动", "address", s.httpServer.Addr, "https_mode", cfg.Server.HTTPSMode)
	}
	return s.httpServer.ListenAndServeTLS(certPath, keyPath)
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
	if s.httpServer == nil {
		return nil
	}
	err := s.httpServer.Shutdown(ctx)
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
	PrivateKeyRef        string   `json:"private_key_ref" binding:"required"`
	HostKeyFingerprint   string   `json:"host_key_fingerprint" binding:"required"`
	CertificatePath      string   `json:"certificate_path"`
	PrivateKeyPath       string   `json:"private_key_path"`
	CertificateChainPath string   `json:"certificate_chain_path"`
	WebServer            string   `json:"web_server" binding:"required"`
	ReloadCommand        []string `json:"reload_command"`
}

type domainServersRequest struct {
	ServerIDs []string `json:"server_ids"`
}

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "scert"})
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
	c.JSON(http.StatusOK, gin.H{
		"service":         "scert",
		"version":         buildinfo.Version,
		"config_version":  cfg.Version,
		"setup_ready":     cfg.SetupReady,
		"https_mode":      cfg.Server.HTTPSMode,
		"database_driver": cfg.Database.Driver,
		"reset_url":       cfg.ResetURL(),
	})
}

func (s *Server) listDomains(c *gin.Context) {
	items, err := s.repository.List(c.Request.Context())
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询域名失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) createDomain(c *gin.Context) {
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
	if err := s.repository.Delete(c.Request.Context(), c.Param("id")); err != nil {
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
	c.JSON(http.StatusOK, gin.H{"certificate": certificateResult, "deployment": deploymentReport})
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
	if err := s.validateSSHKeyReference(c.Request.Context(), item.PrivateKeyRef); err != nil {
		s.writeError(c, http.StatusBadRequest, "SSH 私钥引用无效", err)
		return
	}
	if err := s.servers.CreateServer(c.Request.Context(), item); err != nil {
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
	if err := s.validateSSHKeyReference(c.Request.Context(), item.PrivateKeyRef); err != nil {
		s.writeError(c, http.StatusBadRequest, "SSH 私钥引用无效", err)
		return
	}
	if err := s.servers.UpdateServer(c.Request.Context(), item); err != nil {
		s.writeError(c, serverErrorStatus(err), "更新服务器失败", err)
		return
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

func (s *Server) deleteServer(c *gin.Context) {
	if s.servers == nil {
		s.writeError(c, http.StatusNotImplemented, "服务器管理接口尚未配置", errors.New("服务器仓储未配置"))
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
	c.Status(http.StatusNoContent)
}

func (s *Server) listDomainServers(c *gin.Context) {
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
		Username: request.Username, PrivateKeyRef: request.PrivateKeyRef, HostKeyFingerprint: request.HostKeyFingerprint,
		CertificatePath: request.CertificatePath, PrivateKeyPath: request.PrivateKeyPath,
		CertificateChainPath: request.CertificateChainPath, WebServer: deployment.WebServer(request.WebServer),
		ReloadCommand: request.ReloadCommand,
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
