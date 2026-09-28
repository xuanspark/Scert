package httpapi

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

// 失败审计覆盖会话、来源、参数及仓储拒绝；成功审计由仓储在成员变更事务内保证。
func (s *Server) projectMemberFailureAudit(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if c.Writer.Status() < 400 || s.projectMembers == nil {
			return
		}
		projectID := c.Param("projectID")
		if !domain.ValidProjectID(projectID) {
			projectID = ""
		}
		target := c.GetString(projectMemberTargetKey)
		if target == "" && validMemberIdentifier(c.Param("userID")) {
			target = c.Param("userID")
		}
		// 客户端取消请求不能取消已发生失败的审计；独立等待有上限，不无限阻塞关闭。
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 3*time.Second)
		defer cancel()
		err := s.projectMembers.RecordProjectMemberFailure(ctx, repository.ProjectMemberFailure{
			ActorID: c.GetString(authenticatedActorKey), ProjectID: projectID, UserID: target,
			Action: action, StatusCode: c.Writer.Status(), RequestID: requestIDFromContext(c),
		})
		if err != nil && s.logger != nil {
			// 原失败响应已经确定，不能补发成功；存储故障只记录稳定诊断，不打印请求体和数据库原始错误。
			s.logger.Error("项目成员失败审计未保存", "request_id", requestIDFromContext(c), "status", c.Writer.Status())
		}
	}
}
