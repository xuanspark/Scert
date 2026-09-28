package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func (s *Server) registerProjectLifecycleRoutes() {
	api := s.router.Group("/api/v1/projects", privateProjectResponse)
	api.POST("", s.projectFailureAudit("project.create"), s.requireAuthenticated, s.createProject)
	api.PUT("/:projectID", s.projectFailureAudit("project.update"), s.requireAuthenticated, s.updateProject)
	api.DELETE("/:projectID", s.projectFailureAudit("project.delete"), s.requireAuthenticated, s.deleteProject)
}

func (s *Server) projectLifecycleReady(c *gin.Context, creating bool) bool {
	if s.projectLifecycle == nil {
		s.writeError(c, http.StatusServiceUnavailable, "项目管理服务尚未就绪", nil)
		return false
	}
	if c.Request.URL.RawQuery != "" || (!creating && !domain.ValidProjectID(c.Param("projectID"))) {
		s.writeError(c, http.StatusBadRequest, "项目请求参数无效", nil)
		return false
	}
	return true
}

type projectDetailsRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) projectDetails(c *gin.Context) (projectDetailsRequest, bool) {
	var input projectDetailsRequest
	if err := bindJSON(c, &input, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "项目资料格式无效", nil)
		return input, false
	}
	item := (domain.Project{ID: "validation", Name: input.Name, Description: input.Description}).Normalize()
	if err := item.Validate(); err != nil {
		s.writeError(c, http.StatusBadRequest, err.Error(), nil)
		return input, false
	}
	return projectDetailsRequest{Name: item.Name, Description: item.Description}, true
}

func (s *Server) createProject(c *gin.Context) {
	if !s.projectLifecycleReady(c, true) {
		return
	}
	input, ok := s.projectDetails(c)
	if !ok {
		return
	}
	item, err := s.projectLifecycle.CreateManagedProject(c.Request.Context(), c.GetString(authenticatedActorKey), input.Name, input.Description)
	if err != nil {
		s.writeProjectLifecycleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (s *Server) updateProject(c *gin.Context) {
	if !s.projectLifecycleReady(c, false) {
		return
	}
	input, ok := s.projectDetails(c)
	if !ok {
		return
	}
	if err := s.projectLifecycle.UpdateManagedProject(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("projectID"), input.Name, input.Description); err != nil {
		s.writeProjectLifecycleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "项目资料已更新"})
}

func (s *Server) deleteProject(c *gin.Context) {
	if !s.projectLifecycleReady(c, false) {
		return
	}
	var input struct {
		Confirmation string `json:"confirmation"`
	}
	if err := bindJSON(c, &input, jsonRequestBodyLimit); err != nil || input.Confirmation != c.Param("projectID") {
		s.writeError(c, http.StatusBadRequest, "请确认要删除的项目，确认标识必须与目标项目一致", nil)
		return
	}
	if err := s.projectLifecycle.DeleteManagedProject(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("projectID")); err != nil {
		s.writeProjectLifecycleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) writeProjectLifecycleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrProjectAccessDenied):
		s.writeError(c, http.StatusForbidden, "无权访问项目或执行此操作", nil)
	case errors.Is(err, repository.ErrDefaultProject):
		s.writeError(c, http.StatusConflict, "默认项目不能删除", nil)
	case errors.Is(err, repository.ErrProjectReferenced):
		s.writeError(c, http.StatusConflict, "项目仍有关联资源或成员，请先移出后重试", nil)
	default:
		s.writeError(c, http.StatusInternalServerError, "项目操作失败，请稍后重试", nil)
	}
}

func (s *Server) projectFailureAudit(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if c.Writer.Status() < 400 || s.projectLifecycle == nil {
			return
		}
		projectID := c.Param("projectID")
		if !domain.ValidProjectID(projectID) {
			projectID = ""
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 3*time.Second)
		defer cancel()
		err := s.projectLifecycle.RecordProjectFailure(ctx, repository.ProjectFailure{
			ActorID: c.GetString(authenticatedActorKey), ProjectID: projectID, Action: action,
			StatusCode: c.Writer.Status(), RequestID: requestIDFromContext(c),
		})
		if err != nil && s.logger != nil {
			s.logger.Error("项目失败审计未保存", "request_id", requestIDFromContext(c), "status", c.Writer.Status())
		}
	}
}
