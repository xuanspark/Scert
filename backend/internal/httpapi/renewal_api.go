package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) runRenewalNow(c *gin.Context) {
	if s.runRenewal == nil {
		s.writeError(c, http.StatusNotImplemented, "续期服务未配置", errors.New("续期工作流未配置"))
		return
	}
	if taskRepositoryUnavailable(s.tasks) || s.users == nil {
		s.writeError(c, http.StatusServiceUnavailable, "续期任务或权限服务尚未就绪", nil)
		return
	}
	s.submitRenewalTask(c)
}
