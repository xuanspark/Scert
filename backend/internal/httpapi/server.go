package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/logging"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type Dependencies struct {
	Config     config.Config
	Logger     *logging.Logger
	Repository repository.DomainRepository
}

type Server struct {
	config     config.Config
	logger     *logging.Logger
	repository repository.DomainRepository
	httpServer *http.Server
	router     *gin.Engine
}

func New(deps Dependencies) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	server := &Server{config: deps.Config, logger: deps.Logger, repository: deps.Repository, router: router}
	server.registerRoutes()
	return server
}

func (s *Server) registerRoutes() {
	s.router.GET("/healthz", s.health)
	s.router.GET("/api/v1/status", s.status)
	s.router.GET("/api/v1/domains", s.listDomains)
	s.router.POST("/api/v1/domains", s.createDomain)
	s.router.DELETE("/api/v1/domains/:id", s.deleteDomain)
}

func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) ListenAndServe() error {
	s.httpServer = &http.Server{
		Addr:              net.JoinHostPort(s.config.Server.Host, strconv.Itoa(s.config.Server.Port)),
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if s.logger != nil {
		s.logger.Info("HTTP 服务启动", "address", s.httpServer.Addr, "https_mode", s.config.Server.HTTPSMode)
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

type createDomainRequest struct {
	Name            string   `json:"name" binding:"required"`
	CertificateType string   `json:"certificate_type" binding:"required"`
	SANs            []string `json:"sans"`
}

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "scert"})
}

func (s *Server) status(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"service":         "scert",
		"version":         buildinfo.Version,
		"config_version":  s.config.Version,
		"setup_ready":     s.config.SetupReady,
		"https_mode":      s.config.Server.HTTPSMode,
		"database_driver": s.config.Database.Driver,
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
	var request createDomainRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		s.writeError(c, http.StatusBadRequest, "请求参数无效", err)
		return
	}
	item, err := domain.NewDomain(request.Name, domain.CertificateType(request.CertificateType), request.SANs)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "域名配置无效", err)
		return
	}
	if err := s.repository.Create(c.Request.Context(), item); err != nil {
		s.writeError(c, http.StatusInternalServerError, "保存域名失败", err)
		return
	}
	c.JSON(http.StatusCreated, item)
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
