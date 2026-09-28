package httpapi

import (
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"github.com/gin-gonic/gin"
)

// 归属由服务端会话填写，不能从请求体接受操作者或项目授权声明。
func (s *Server) newResourceScope(c *gin.Context) domain.ResourceScope {
	scope := domain.ResourceScope{ProjectID: domain.DefaultProjectID}
	if session, ok := s.currentAuthSession(c); ok {
		scope.OwnerID = session.UserID
		scope.CreatedBy = session.UserID
		scope.UpdatedBy = session.UserID
	}
	return scope
}

func (s *Server) updatedResourceScope(c *gin.Context, existing domain.ResourceScope) domain.ResourceScope {
	existing.UpdatedBy = ""
	if session, ok := s.currentAuthSession(c); ok {
		existing.UpdatedBy = session.UserID
	}
	return existing.Normalize()
}
