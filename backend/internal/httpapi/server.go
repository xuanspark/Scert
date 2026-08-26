package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/logging"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type Dependencies struct {
	Config     config.Config
	ConfigPath string
	Logger     *logging.Logger
	Repository repository.DomainRepository
	SetupDNS   setupDNSVerifier
	Firewall   firewall.Manager
}

type Server struct {
	configMu   sync.RWMutex
	config     config.Config
	configPath string
	logger     *logging.Logger
	repository repository.DomainRepository
	setupDNS   setupDNSVerifier
	firewall   firewall.Manager
	httpServer *http.Server
	router     *gin.Engine
}

func New(deps Dependencies) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	server := &Server{
		config: deps.Config, configPath: deps.ConfigPath, logger: deps.Logger,
		repository: deps.Repository, setupDNS: deps.SetupDNS, firewall: deps.Firewall, router: router,
	}
	server.registerRoutes()
	server.registerSetupRoutes()
	return server
}

func (s *Server) registerRoutes() {
	// 主路由保持独立，初始化路由由专用模块注册。
	s.router.GET("/healthz", s.health)
	s.router.GET("/api/v1/status", s.status)
	s.router.GET("/api/v1/domains", s.listDomains)
	s.router.POST("/api/v1/domains", s.createDomain)
	s.router.PUT("/api/v1/domains/:id", s.updateDomain)
	s.router.DELETE("/api/v1/domains/:id", s.deleteDomain)
}

func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) ListenAndServe() error {
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	s.httpServer = &http.Server{
		Addr:              net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if s.logger != nil {
		s.logger.Info("HTTP 服务启动", "address", s.httpServer.Addr, "https_mode", cfg.Server.HTTPSMode)
	}
	return s.httpServer.ListenAndServe()
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

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "scert"})
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
	if err := c.ShouldBindJSON(&request); err != nil {
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
	if err := c.ShouldBindJSON(&request); err != nil {
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

func (s *Server) writeError(c *gin.Context, status int, message string, err error) {
	if s.logger != nil {
		s.logger.Error(message, "error", err)
	}
	c.JSON(status, gin.H{"error": message})
}
