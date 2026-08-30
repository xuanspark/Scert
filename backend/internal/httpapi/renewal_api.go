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
	response := gin.H{"report": report}
	if err != nil {
		response["error"] = err.Error()
		c.JSON(http.StatusConflict, response)
		return
	}
	c.JSON(http.StatusOK, response)
}
