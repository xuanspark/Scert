package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func (s *Server) registerProjectResourceRoutes() {
	api := s.router.Group("/api/v1", privateProjectResponse, s.requireAuthenticated)
	api.GET("/domains", s.listDomains)
	api.GET("/audit-logs", s.listAuditLogs)
	api.GET("/servers", s.listServers)
	api.GET("/servers/:id", s.getServer)
	api.GET("/domains/:id/servers", s.listDomainServers)
	api.GET("/certificates", s.listCertificates)
	api.GET("/certificates/:id", s.getCertificate)
	api.GET("/domains/:id/certificate/versions", s.listCertificateVersions)
}

func (s *Server) requireProjectResourceReader(c *gin.Context) bool {
	if s.projectResources == nil {
		s.writeError(c, http.StatusServiceUnavailable, "项目资源服务尚未就绪", nil)
		return false
	}
	return true
}

func (s *Server) writeProjectResourceError(c *gin.Context, err error) {
	if errors.Is(err, repository.ErrProjectAccessDenied) {
		// 无权限和不存在使用相同响应，不泄露其他项目的资源是否存在。
		s.writeError(c, http.StatusForbidden, "无权读取此项目资源", nil)
		return
	}
	s.writeError(c, http.StatusInternalServerError, "读取项目资源失败，请稍后重试", nil)
}

func (s *Server) requireVisibleCertificateDomain(c *gin.Context) bool {
	if !s.requireProjectResourceReader(c) {
		return false
	}
	if _, err := s.projectResources.GetVisibleDomain(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id")); err != nil {
		s.writeProjectResourceError(c, err)
		return false
	}
	return true
}

func (s *Server) authorizeVisibleDomainPermission(c *gin.Context, permission domain.ProjectPermission) bool {
	if !s.requireProjectResourceReader(c) {
		return false
	}
	item, err := s.projectResources.GetVisibleDomain(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		s.writeProjectResourceError(c, err)
		return false
	}
	authorizer, ok := s.projectDomains.(repository.ProjectAuthorizer)
	if !ok {
		if fallback, fallbackOK := s.projectMembers.(repository.ProjectAuthorizer); fallbackOK {
			authorizer = fallback
			ok = true
		}
	}
	if !ok {
		s.writeProjectResourceError(c, errors.New("project authorizer unavailable"))
		return false
	}
	if err := authorizer.AuthorizeProject(c.Request.Context(), c.GetString(authenticatedActorKey), item.ProjectID, permission); err != nil {
		s.writeProjectResourceError(c, err)
		return false
	}
	return true
}

func (s *Server) authorizeDNSCredentialUse(c *gin.Context, projectID string) bool {
	authorizer, ok := s.projectMembers.(repository.ProjectAuthorizer)
	if !ok {
		authorizer, ok = s.projectDomains.(repository.ProjectAuthorizer)
	}
	if !ok {
		s.writeError(c, http.StatusServiceUnavailable, "项目 DNS 权限服务尚未就绪", nil)
		return false
	}
	if err := authorizer.AuthorizeProject(c.Request.Context(), c.GetString(authenticatedActorKey), projectID, domain.PermissionDNSCredentialUse); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权使用此项目的 DNS 凭据", nil)
			return false
		}
		s.writeError(c, http.StatusInternalServerError, "检查项目 DNS 凭据权限失败", err)
		return false
	}
	return true
}
