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
	report, err := s.runRenewal(c.Request.Context())
	if len(report.Errors) > 0 {
		// 保留失败数量，不将证书路径、凭据或下游 SDK 响应带回浏览器。
		messages := make([]string, len(report.Errors))
		for index := range messages {
			messages[index] = "证书续期失败，请检查对应任务及受保护的服务日志"
		}
		report.Errors = messages
	}
	response := gin.H{"report": report}
	if err != nil {
		if s.logger != nil {
			s.logger.Error("执行证书续期失败", "error", err)
		}
		response["error"] = "执行证书续期失败"
		c.JSON(http.StatusConflict, response)
		return
	}
	c.JSON(http.StatusOK, response)
}
