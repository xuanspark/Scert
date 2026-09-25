package httpapi

import (
	"net/http"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"github.com/gin-gonic/gin"
)

// userListViewer 每次从仓储读取当前分组，避免调组后沿用会话中的旧权限。
func (s *Server) userListViewer(c *gin.Context) (repository.User, bool) {
	session, ok := s.currentAuthSession(c)
	if !ok {
		s.writeError(c, http.StatusUnauthorized, "请先登录管理后台", nil)
		return repository.User{}, false
	}
	if session.Role == repository.UserRoleSuperAdmin {
		return repository.User{ID: session.UserID, Role: session.Role}, true
	}
	viewer, err := s.users.GetUserByID(c.Request.Context(), session.UserID)
	if err != nil || viewer.Status != repository.UserStatusActive || viewer.Role != session.Role {
		s.writeError(c, http.StatusForbidden, "当前账号无法读取用户列表", nil)
		return repository.User{}, false
	}
	return viewer, true
}

func userVisibleTo(viewer, item repository.User) bool {
	if viewer.Role == repository.UserRoleSuperAdmin || viewer.ID == item.ID {
		return true
	}
	if viewer.Role != repository.UserRoleAdmin || item.Role == repository.UserRoleSuperAdmin {
		return false
	}
	if len(viewer.Groups) == 0 && len(item.Groups) == 0 {
		return true
	}
	for _, group := range viewer.Groups {
		for _, memberGroup := range item.Groups {
			if group == memberGroup {
				return true
			}
		}
	}
	return false
}

// scopedUserGroups 不向普通用户输出分组，也不向管理员泄露其他分组名称。
func scopedUserGroups(viewer, item repository.User) repository.User {
	if viewer.Role == repository.UserRoleSuperAdmin {
		return item
	}
	groups := make([]string, 0)
	if viewer.Role == repository.UserRoleAdmin {
		for _, group := range item.Groups {
			for _, allowed := range viewer.Groups {
				if group == allowed {
					groups = append(groups, group)
					break
				}
			}
		}
	}
	item.Groups = groups
	return item
}

func (s *Server) applyRequestedUserGroups(c *gin.Context, item *repository.User, requested *[]string) bool {
	groups := item.Groups
	if requested != nil {
		groups = *requested
	}
	var err error
	item.Groups, err = repository.NormalizeUserGroups(item.Role, groups)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, err.Error(), nil)
		return false
	}
	return true
}
