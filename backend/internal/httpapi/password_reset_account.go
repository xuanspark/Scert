package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/auth"
)

const passwordResetResolveBodyLimit = 16 * 1024

type passwordResetResolveRequest struct {
	Identifier string `json:"identifier"`
}

type passwordResetResolution struct {
	AccountType  string `json:"account_type"`
	AccountLabel string `json:"account_label"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Action       string `json:"action"`
}

// resolvePasswordResetAccount 根据账户标识返回对应的密码处理内容。
func (s *Server) resolvePasswordResetAccount(c *gin.Context) {
	var request passwordResetResolveRequest
	if err := bindJSON(c, &request, passwordResetResolveBodyLimit); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "账户信息格式无效"})
		return
	}

	identifier, err := auth.NormalizeIdentifier(request.Identifier)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入有效的用户名或绑定邮箱"})
		return
	}

	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	if !cfg.SetupReady {
		c.JSON(http.StatusConflict, gin.H{"error": "Scert 尚未完成初始化，暂不能处理密码重置"})
		return
	}

	if auth.IsMatch(identifier, cfg.Admin.Username, cfg.Admin.Email) {
		c.JSON(http.StatusOK, passwordResetResolution{
			AccountType:  "super_admin",
			AccountLabel: "超级管理员",
			Title:        "超级管理员密码重置",
			Description:  "已识别为超级管理员账户。该账户不能通过普通重置页面修改密码，请联系系统管理员按照服务器本机安全维护流程处理。",
			Action:       "contact_administrator",
		})
		return
	}

	// 未识别账户时不返回具体账户信息，避免通过重置入口枚举账户。
	c.JSON(http.StatusOK, passwordResetResolution{
		AccountType:  "unknown",
		AccountLabel: "未识别账户",
		Title:        "无法识别账户",
		Description:  "未找到匹配的账户，请检查用户名或绑定邮箱；如果账户属于其他用户类型，请联系有权限的管理员处理。",
		Action:       "check_identifier",
	})
}
