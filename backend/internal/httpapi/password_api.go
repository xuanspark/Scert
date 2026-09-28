package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/auth"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/passwordpolicy"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

// changePassword 允许当前登录账号修改自己的密码，并在成功后使所有旧会话失效。
func (s *Server) changePassword(c *gin.Context) {
	request := changePasswordRequest{}
	if err := bindJSON(c, &request, authRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "修改密码请求参数无效", err)
		return
	}
	if request.CurrentPassword == "" {
		s.writeError(c, http.StatusBadRequest, "请输入当前密码", nil)
		return
	}
	if request.NewPassword != request.ConfirmPassword {
		s.writeError(c, http.StatusBadRequest, "两次输入的新密码不一致", nil)
		return
	}

	session, ok := s.currentAuthSession(c)
	if !ok {
		s.writeError(c, http.StatusUnauthorized, "登录状态已失效，请重新登录", nil)
		return
	}
	if session.UserID == "primary-super-admin" {
		s.changePrimarySuperAdminPassword(c, session, request)
		return
	}
	s.changeDatabaseUserPassword(c, session, request)
}

func (s *Server) changePrimarySuperAdminPassword(c *gin.Context, session authSession, request changePasswordRequest) {
	if !s.tlsMu.TryLock() {
		s.writeError(c, http.StatusConflict, "管理证书或配置正在更新，请稍后重试", nil)
		return
	}
	defer s.tlsMu.Unlock()
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	if _, err := auth.AuthenticateAdmin(session.Username, request.CurrentPassword, cfg.Admin.Username, cfg.Admin.Email, cfg.Admin.PasswordHash); err != nil {
		s.writeError(c, http.StatusUnauthorized, "当前密码错误", nil)
		return
	}
	if err := passwordpolicy.Validate(request.NewPassword, cfg.Admin.Username); err != nil {
		s.writeError(c, http.StatusBadRequest, err.Error(), err)
		return
	}
	if strings.TrimSpace(s.configPath) == "" {
		s.writeError(c, http.StatusInternalServerError, "服务配置路径未就绪，无法保存新密码", errors.New("配置路径未配置"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(request.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "生成新密码摘要失败", err)
		return
	}
	persistedConfig, err := config.Load(s.configPath)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "读取服务配置失败，无法保存新密码", err)
		return
	}
	previousConfig := persistedConfig
	persistedConfig.Admin.PasswordHash = string(hash)
	if err := s.syncPrimarySuperAdmin(c.Request.Context(), persistedConfig); err != nil {
		s.writeError(c, http.StatusInternalServerError, "同步超级管理员账户失败", err)
		return
	}
	if err := persistedConfig.Save(s.configPath); err != nil {
		if restoreErr := s.syncPrimarySuperAdmin(c.Request.Context(), previousConfig); restoreErr != nil && s.logger != nil {
			s.logger.Error("保存新密码失败后恢复超级管理员摘要失败", "error", restoreErr)
		}
		s.writeError(c, http.StatusInternalServerError, "保存新密码失败", err)
		return
	}
	cfg.Admin.PasswordHash = string(hash)
	s.configMu.Lock()
	s.config = cfg
	s.configMu.Unlock()
	s.finishPasswordChange(c, session.UserID, "超级管理员密码修改成功，请使用新密码重新登录")
}

func (s *Server) changeDatabaseUserPassword(c *gin.Context, session authSession, request changePasswordRequest) {
	if s.users == nil {
		s.writeError(c, http.StatusNotImplemented, "用户仓储尚未配置", errors.New("用户仓储未配置"))
		return
	}
	user, err := s.users.GetUserByID(c.Request.Context(), session.UserID)
	if err != nil {
		s.writeError(c, http.StatusUnauthorized, "当前登录账号已不可用，请重新登录", err)
		return
	}
	if user.Status != repository.UserStatusActive {
		s.writeError(c, http.StatusForbidden, "当前账号已被禁用", nil)
		return
	}
	if _, err := auth.AuthenticateAccount(session.Username, request.CurrentPassword, user.Username, user.Email, user.PasswordHash, user.Role); err != nil {
		s.writeError(c, http.StatusUnauthorized, "当前密码错误", nil)
		return
	}
	if err := passwordpolicy.ValidateAccountPassword(request.NewPassword, user.Username); err != nil {
		s.writeError(c, http.StatusBadRequest, err.Error(), err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(request.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "生成新密码摘要失败", err)
		return
	}
	user.PasswordHash = string(hash)
	if err := s.users.UpdateUser(c.Request.Context(), user); err != nil {
		s.writeError(c, http.StatusInternalServerError, "保存新密码失败", err)
		return
	}
	s.finishPasswordChange(c, session.UserID, "密码修改成功，请使用新密码重新登录")
}

func (s *Server) finishPasswordChange(c *gin.Context, userID, message string) {
	s.invalidateUserSessions(userID)
	s.clearAuthCookie(c)
	s.recordAuthAudit(c, userID, "修改密码", "success", "当前登录账号修改密码成功")
	c.JSON(http.StatusOK, gin.H{
		"authenticated":  false,
		"requires_login": true,
		"message":        message,
	})
}
