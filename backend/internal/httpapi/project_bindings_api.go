package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func (s *Server) registerProjectBindingRoutes() {
	api := s.router.Group("/api/v1", privateProjectResponse)
	api.PUT("/domains/:id/servers", s.projectBindingFailureAudit, s.requireAuthenticated, s.replaceProjectDomainServers)
}

func (s *Server) replaceProjectDomainServers(c *gin.Context) {
	if s.projectBindings == nil {
		s.writeError(c, http.StatusServiceUnavailable, "项目绑定服务尚未就绪", nil)
		return
	}
	var input struct {
		ServerIDs *[]string `json:"server_ids"`
	}
	if !validMemberIdentifier(c.Param("id")) || c.Request.URL.RawQuery != "" ||
		bindJSON(c, &input, jsonRequestBodyLimit) != nil || input.ServerIDs == nil || len(*input.ServerIDs) > repository.MaxProjectBindingServers {
		s.writeError(c, http.StatusBadRequest, repository.ErrProjectBindingInput.Error(), nil)
		return
	}
	for _, id := range *input.ServerIDs {
		if !validMemberIdentifier(id) {
			s.writeError(c, http.StatusBadRequest, repository.ErrProjectBindingInput.Error(), nil)
			return
		}
	}
	items, err := s.projectBindings.ReplaceProjectDomainServers(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"), *input.ServerIDs)
	switch {
	case errors.Is(err, repository.ErrProjectAccessDenied):
		s.writeError(c, http.StatusForbidden, "无权修改此项目的服务器绑定", nil)
	case errors.Is(err, repository.ErrProjectBindingInput):
		s.writeError(c, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, deployment.ErrSharedPathConflict):
		s.writeError(c, http.StatusConflict, deployment.ErrSharedPathConflict.Error(), nil)
	case err != nil:
		s.writeError(c, http.StatusInternalServerError, "保存项目服务器绑定失败，请刷新后核对", nil)
	default:
		c.JSON(http.StatusOK, gin.H{"items": items})
	}
}

func (s *Server) projectBindingFailureAudit(c *gin.Context) {
	c.Next()
	if c.Writer.Status() < 400 || s.projectBindings == nil {
		return
	}
	domainID := c.Param("id")
	if !validMemberIdentifier(domainID) {
		domainID = ""
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 3*time.Second)
	defer cancel()
	err := s.projectBindings.RecordProjectBindingFailure(ctx, repository.ProjectBindingFailure{
		ActorID: c.GetString(authenticatedActorKey), DomainID: domainID,
		StatusCode: c.Writer.Status(), RequestID: requestIDFromContext(c),
	})
	if err != nil && s.logger != nil {
		s.logger.Error("项目绑定失败审计未保存", "request_id", requestIDFromContext(c), "status", c.Writer.Status())
	}
}
