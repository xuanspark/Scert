package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

const projectMemberTargetKey = "project_member_target_id"

func (s *Server) registerProjectMemberRoutes() {
	api := s.router.Group("/api/v1/projects/:projectID/members")
	api.GET("", s.projectMemberFailureAudit("project_member.list"), s.requireAuthenticated, s.listProjectMembers)
	api.POST("", s.projectMemberFailureAudit("project_member.grant"), s.requireAuthenticated, s.grantProjectMember)
	api.DELETE("/:userID/roles/:role", s.projectMemberFailureAudit("project_member.revoke"), s.requireAuthenticated, s.revokeProjectMember)
}

func (s *Server) projectMemberRequestReady(c *gin.Context) bool {
	if s.projectMembers == nil {
		s.writeError(c, http.StatusServiceUnavailable, "项目成员服务尚未就绪", nil)
		return false
	}
	if !validMemberIdentifier(c.Param("projectID")) {
		s.writeError(c, http.StatusBadRequest, "项目标识无效", nil)
		return false
	}
	return true
}

func (s *Server) listProjectMembers(c *gin.Context) {
	if !s.projectMemberRequestReady(c) {
		return
	}
	query, err := projectMemberPageQuery(c)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "分页参数无效，每页仅支持 5、10、15 或 30 位成员", nil)
		return
	}
	page, err := s.projectMembers.ListProjectMembers(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("projectID"), query.After, query.Size)
	if err != nil {
		s.writeProjectMemberError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (s *Server) grantProjectMember(c *gin.Context) {
	if !s.projectMemberRequestReady(c) {
		return
	}
	var request struct {
		UserID string             `json:"user_id"`
		Role   domain.ProjectRole `json:"role"`
	}
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil || !validMemberIdentifier(request.UserID) || !request.Role.Valid() || c.Request.URL.RawQuery != "" {
		s.writeError(c, http.StatusBadRequest, "项目成员参数无效", nil)
		return
	}
	c.Set(projectMemberTargetKey, request.UserID)
	if err := s.projectMembers.GrantProjectRole(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("projectID"), request.UserID, request.Role); err != nil {
		s.writeProjectMemberError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "项目角色已授予"})
}

func (s *Server) revokeProjectMember(c *gin.Context) {
	if !s.projectMemberRequestReady(c) {
		return
	}
	role := domain.ProjectRole(c.Param("role"))
	if c.Request.Body != nil {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1))
		if err != nil || len(body) != 0 {
			s.writeError(c, http.StatusBadRequest, "撤销角色请求不接受请求体", nil)
			return
		}
	}
	if !validMemberIdentifier(c.Param("userID")) || !role.Valid() || c.Request.URL.RawQuery != "" {
		s.writeError(c, http.StatusBadRequest, "项目成员参数无效", nil)
		return
	}
	c.Set(projectMemberTargetKey, c.Param("userID"))
	if err := s.projectMembers.RevokeProjectRole(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("projectID"), c.Param("userID"), role); err != nil {
		s.writeProjectMemberError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) writeProjectMemberError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrProjectAccessDenied):
		// 不区分无权限、项目不存在或目标不满足条件，避免泄露其他项目与账户信息。
		s.writeError(c, http.StatusForbidden, "无权访问项目或执行此操作", nil)
	case errors.Is(err, repository.ErrProjectMemberConflict):
		s.writeError(c, http.StatusConflict, "项目成员关系冲突，请刷新后重试", nil)
	default:
		s.writeError(c, http.StatusInternalServerError, "项目成员操作失败，请稍后重试", nil)
	}
}

type projectMemberQuery struct {
	After string
	Size  int
}

func projectMemberPageQuery(c *gin.Context) (projectMemberQuery, error) {
	result := projectMemberQuery{Size: 10}
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return result, err
	}
	for key, entries := range values {
		if (key != "after" && key != "page_size") || len(entries) != 1 {
			return result, errors.New("分页参数重复或未知")
		}
	}
	if values.Has("page_size") {
		result.Size, err = strconv.Atoi(values.Get("page_size"))
		if err != nil {
			return result, err
		}
	}
	switch result.Size {
	case 5, 10, 15, 30:
	default:
		return result, errors.New("每页成员数量无效")
	}
	result.After = values.Get("after")
	if result.After != "" && !validMemberIdentifier(result.After) {
		return result, errors.New("分页游标无效")
	}
	return result, nil
}

func validMemberIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}
