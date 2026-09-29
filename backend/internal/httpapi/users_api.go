package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/passwordpolicy"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type userRequest struct {
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Password string    `json:"password"`
	Role     string    `json:"role"`
	Status   string    `json:"status"`
	Groups   *[]string `json:"groups"`
}

func (s *Server) requireSuperAdmin(c *gin.Context) {
	s.requireSuperAdminAction(c, "管理用户")
}

func (s *Server) requireSuperAdminSystemConfig(c *gin.Context) {
	s.requireSuperAdminAction(c, "管理系统设置")
}

func (s *Server) requireSuperAdminAction(c *gin.Context, action string) {
	s.configMu.RLock()
	setupReady := s.config.SetupReady
	s.configMu.RUnlock()
	if !setupReady {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error":      "请先完成初始化配置",
			"request_id": requestIDFromContext(c),
		})
		return
	}
	session, ok := s.currentAuthSession(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请先登录管理后台", "request_id": requestIDFromContext(c)})
		return
	}
	if session.Role != repository.UserRoleSuperAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "只有超级管理员可以" + action, "request_id": requestIDFromContext(c)})
		return
	}
	if !s.sameOriginForAPI(c.Request) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "请求来源校验失败", "request_id": requestIDFromContext(c)})
		return
	}
	c.Next()
}

func (s *Server) listUsers(c *gin.Context) {
	if s.users == nil {
		s.writeError(c, http.StatusNotImplemented, "用户管理接口尚未配置", errors.New("用户仓储未配置"))
		return
	}
	viewer, ok := s.userListViewer(c)
	if !ok {
		return
	}
	items, err := s.users.ListUsers(c.Request.Context())
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询用户失败", err)
		return
	}
	visible := make([]repository.User, 0, len(items))
	for _, item := range items {
		if userVisibleTo(viewer, item) {
			visible = append(visible, scopedUserGroups(viewer, item))
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": visible, "current_user_id": viewer.ID, "current_role": viewer.Role})
}

func (s *Server) getUser(c *gin.Context) {
	if s.users == nil {
		s.writeError(c, http.StatusNotImplemented, "用户管理接口尚未配置", errors.New("用户仓储未配置"))
		return
	}
	viewer, ok := s.userListViewer(c)
	if !ok {
		return
	}
	item, err := s.users.GetUserByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "用户不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取用户失败", err)
		return
	}
	if !userVisibleTo(viewer, item) {
		s.writeError(c, http.StatusNotFound, "用户不存在", nil)
		return
	}
	c.JSON(http.StatusOK, scopedUserGroups(viewer, item))
}

func (s *Server) createUser(c *gin.Context) {
	if s.users == nil {
		s.writeError(c, http.StatusNotImplemented, "用户管理接口尚未配置", errors.New("用户仓储未配置"))
		return
	}
	var request userRequest
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "用户参数无效", err)
		return
	}
	item, err := userFromRequest(request, "")
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "用户参数无效", err)
		return
	}
	if item.Role == repository.UserRoleSuperAdmin {
		s.writeError(c, http.StatusBadRequest, "系统只允许初始化时创建一个超级管理员", nil)
		return
	}
	if !s.applyRequestedUserGroups(c, &item, request.Groups) {
		return
	}
	if err := s.users.CreateUser(c.Request.Context(), item); err != nil {
		s.writeError(c, userErrorStatus(err), "创建用户失败", err)
		return
	}
	s.recordUserAudit(c, s.currentOperatorID(c), "创建用户", "success", item.ID)
	c.JSON(http.StatusCreated, item)
}

func (s *Server) updateUser(c *gin.Context) {
	if s.users == nil {
		s.writeError(c, http.StatusNotImplemented, "用户管理接口尚未配置", errors.New("用户仓储未配置"))
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "primary-super-admin" {
		s.writeError(c, http.StatusBadRequest, "主超级管理员不能通过用户管理修改", nil)
		return
	}
	existing, err := s.users.GetUserByID(c.Request.Context(), id)
	if err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "用户不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取用户失败", err)
		return
	}
	var request userRequest
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "用户参数无效", err)
		return
	}
	item, err := userFromRequest(request, id)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "用户参数无效", err)
		return
	}
	if request.Password == "" {
		item.PasswordHash = existing.PasswordHash
	}
	if request.Role == "" {
		item.Role = existing.Role
	}
	if request.Status == "" {
		item.Status = existing.Status
	}
	item.Groups = existing.Groups
	if !s.applyRequestedUserGroups(c, &item, request.Groups) {
		return
	}
	if existing.Role != repository.UserRoleSuperAdmin && item.Role == repository.UserRoleSuperAdmin {
		s.writeError(c, http.StatusBadRequest, "不能将用户提升为超级管理员", nil)
		return
	}
	if existing.Role == repository.UserRoleSuperAdmin && item.Role != repository.UserRoleSuperAdmin {
		s.writeError(c, http.StatusBadRequest, "不能降低超级管理员权限", nil)
		return
	}
	if existing.Role == repository.UserRoleSuperAdmin && item.Status == repository.UserStatusDisabled {
		if err := s.ensureAnotherSuperAdmin(c, id); err != nil {
			s.writeError(c, http.StatusBadRequest, err.Error(), err)
			return
		}
	}
	// 更新自身账户会使原会话失效，审计身份必须在写入前从已验证会话读取。
	operatorID := s.currentOperatorID(c)
	if err := s.users.UpdateUser(c.Request.Context(), item); err != nil {
		s.writeError(c, userErrorStatus(err), "更新用户失败", err)
		return
	}
	s.invalidateUserSessions(id)
	s.recordUserAudit(c, operatorID, "更新用户", "success", id)
	c.JSON(http.StatusOK, item)
}

func (s *Server) deleteUser(c *gin.Context) {
	if s.users == nil {
		s.writeError(c, http.StatusNotImplemented, "用户管理接口尚未配置", errors.New("用户仓储未配置"))
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	session, _ := s.currentAuthSession(c)
	if session.UserID != "" && session.UserID == id {
		s.writeError(c, http.StatusBadRequest, "不能删除当前登录用户", nil)
		return
	}
	existing, err := s.users.GetUserByID(c.Request.Context(), id)
	if err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "用户不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取用户失败", err)
		return
	}
	if existing.Role == repository.UserRoleSuperAdmin {
		if existing.ID == "primary-super-admin" {
			s.writeError(c, http.StatusBadRequest, "不能删除配置中的主超级管理员", repository.ErrSuperAdminProtected)
			return
		}
		if err := s.ensureAnotherSuperAdmin(c, id); err != nil {
			s.writeError(c, http.StatusBadRequest, err.Error(), err)
			return
		}
	}
	operatorID := s.currentOperatorID(c)
	if err := s.users.DeleteUser(c.Request.Context(), id); err != nil {
		if errors.Is(err, repository.ErrSuperAdminProtected) {
			s.writeError(c, http.StatusBadRequest, err.Error(), err)
			return
		}
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "用户不存在", err)
			return
		}
		s.writeError(c, userErrorStatus(err), "删除用户失败", err)
		return
	}
	s.invalidateUserSessions(id)
	s.recordUserAudit(c, operatorID, "删除用户", "success", id)
	c.Status(http.StatusNoContent)
}

func (s *Server) ensureAnotherSuperAdmin(c *gin.Context, currentID string) error {
	items, err := s.users.ListUsers(c.Request.Context())
	if err != nil {
		return errors.New("无法确认超级管理员数量")
	}
	for _, item := range items {
		if item.Role == repository.UserRoleSuperAdmin && item.Status == repository.UserStatusActive && item.ID != currentID {
			return nil
		}
	}
	return errors.New("不能禁用最后一个超级管理员")
}

func userFromRequest(request userRequest, id string) (repository.User, error) {
	username := strings.TrimSpace(strings.ToLower(request.Username))
	if username == "" {
		return repository.User{}, errors.New("用户名不能为空")
	}
	if request.Password == "" && id == "" {
		return repository.User{}, errors.New("密码不能为空")
	}
	item := repository.User{
		ID: id, Username: username, Email: request.Email,
		Role: strings.TrimSpace(strings.ToLower(request.Role)), Status: strings.TrimSpace(strings.ToLower(request.Status)),
	}
	if item.Role == "" {
		item.Role = repository.UserRoleUser
	}
	if item.Status == "" {
		item.Status = repository.UserStatusActive
	}
	if request.Password != "" {
		if err := passwordpolicy.ValidateAccountPassword(request.Password, username); err != nil {
			return repository.User{}, err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
		if err != nil {
			return repository.User{}, errors.New("生成密码摘要失败")
		}
		item.PasswordHash = string(hash)
	}
	if item.ID == "" {
		item.ID = domain.NewID()
	}
	return item, nil
}

func (s *Server) recordUserAudit(c *gin.Context, operatorID, action, status, id string) {
	if s.audit == nil {
		return
	}
	if err := s.audit.CreateAuditLog(c.Request.Context(), repository.AuditLog{
		OperatorID: operatorID,
		Action:     action, ResourceType: "user", ResourceID: id, Status: status, Message: action,
	}); err != nil && s.logger != nil {
		s.logger.Warn("保存用户审计日志失败", "error", err)
	}
}

func userErrorStatus(err error) int {
	if repository.IsConflict(err) {
		return http.StatusConflict
	}
	if repository.IsNotFound(err) {
		return http.StatusNotFound
	}
	if strings.Contains(err.Error(), "不能为空") || strings.Contains(err.Error(), "无效") || strings.Contains(err.Error(), "长度") || strings.Contains(err.Error(), "不能") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
