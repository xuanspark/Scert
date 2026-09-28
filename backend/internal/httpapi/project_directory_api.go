package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

// 项目与成员数据属于当前身份，即使认证失败也不能让共享缓存复用响应。
func privateProjectResponse(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Cookie")
	c.Next()
}

func (s *Server) listVisibleProjects(c *gin.Context) {
	if s.projectDirectory == nil {
		s.writeError(c, http.StatusServiceUnavailable, "项目服务尚未就绪", nil)
		return
	}
	query, err := projectMemberPageQuery(c)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "分页参数无效，每页仅支持 5、10、15 或 30 个项目", nil)
		return
	}
	page, err := s.projectDirectory.ListVisibleProjects(c.Request.Context(), c.GetString(authenticatedActorKey), query.After, query.Size)
	if err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权读取项目列表", nil)
		} else {
			s.writeError(c, http.StatusInternalServerError, "读取项目列表失败，请稍后重试", nil)
		}
		return
	}
	c.JSON(http.StatusOK, page)
}
