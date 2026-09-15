package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"github.com/gin-gonic/gin"
)

const operationResponseGrace = 2 * time.Minute

// 仅为明确的长操作延长响应期限，普通请求仍受服务器默认写超时保护。
// 工作流使用有限上下文，额外响应时间留给 DNS 清理、部署回滚和结果序列化。
func (s *Server) longOperation(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		deadline, _ := ctx.Deadline()
		controller := http.NewResponseController(c.Writer)
		if err := controller.SetWriteDeadline(deadline.Add(operationResponseGrace)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			s.writeError(c, http.StatusServiceUnavailable, "无法设置长操作响应期限，请稍后重试", err)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func (s *Server) dnsOperation(c *gin.Context) {
	// 路由中间件在绑定 JSON 前无法可靠识别服务商，保留兼容服务商的最长响应时限。
	// testDNS 会在参数校验后按服务商建立更精确的工作上下文。
	s.longOperation(dnsprovider.PermissionVerificationTimeout)(c)
}

func (s *Server) requireSameOrigin(c *gin.Context) {
	if !s.sameOriginForAPI(c.Request) {
		s.writeError(c, http.StatusForbidden, "请求来源校验失败", nil)
		c.Abort()
		return
	}
	c.Next()
}

func (s *Server) requireSetupAccess(c *gin.Context) {
	if !s.allowSetupRequest(c) {
		c.Abort()
		return
	}
	c.Next()
}
