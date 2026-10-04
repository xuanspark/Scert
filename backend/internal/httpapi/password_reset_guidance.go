package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/auth"
)

type publicPasswordResetGuidance struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Action      string `json:"action"`
}

// showPasswordResetGuidance 仅验证输入格式，不查询账户，也不创建重置会话。
// 公开路由必须使用此处理器，不能绑定历史的账户分类处理器。
func (s *Server) showPasswordResetGuidance(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var request passwordResetResolveRequest
	if err := bindJSON(c, &request, passwordResetResolveBodyLimit); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "账户信息格式无效"})
		return
	}
	if _, err := auth.NormalizeIdentifier(request.Identifier); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入有效的用户名或绑定邮箱"})
		return
	}

	s.configMu.RLock()
	ready := s.config.SetupReady
	s.configMu.RUnlock()
	if !ready {
		c.JSON(http.StatusConflict, gin.H{"error": "Scert 尚未完成初始化，暂不能处理密码重置"})
		return
	}

	c.JSON(http.StatusOK, publicPasswordResetGuidance{
		Title:       "密码重置",
		Description: "当前方式无法使用，请联系运维人员辅助解决。",
		Action:      "contact_administrator",
	})
}
