package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

var ErrProjectAccessDenied = errors.New("无权访问项目或执行此操作")

type ProjectAuthorizer interface {
	AuthorizeProject(context.Context, string, string, domain.ProjectPermission) error
}

type SQLProjectMemberRepository struct {
	db     *sql.DB
	driver string
}

var _ ProjectAuthorizer = (*SQLProjectMemberRepository)(nil)

func NewSQLProjectMemberRepository(db *sql.DB, driver ...string) *SQLProjectMemberRepository {
	return &SQLProjectMemberRepository{db: db, driver: repositoryDriver(driver)}
}

func (r *SQLProjectMemberRepository) ensureDB() error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	return nil
}

// AuthorizeProject 每次从数据库读取账户、项目和成员关系，不信任请求中的角色或用户分组。
// 这里只判定指定项目，业务入口仍须从真实资源取得项目，并在执行副作用前重新判权。
func (r *SQLProjectMemberRepository) AuthorizeProject(ctx context.Context, userID, projectID string, permission domain.ProjectPermission) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	if !permission.Valid() || strings.TrimSpace(userID) == "" || strings.TrimSpace(projectID) == "" {
		return ErrProjectAccessDenied
	}
	rows, err := r.db.QueryContext(ctx, Rebind(r.driver, `SELECT u.role, u.status, COALESCE(m.role, '')
		FROM users u JOIN projects p ON p.id = ?
		LEFT JOIN project_members m ON m.project_id = p.id AND m.user_id = u.id
		WHERE u.id = ?`), strings.TrimSpace(projectID), strings.TrimSpace(userID))
	if err != nil {
		return fmt.Errorf("读取项目权限失败：%w", err)
	}
	defer rows.Close()
	allowed := false
	for rows.Next() {
		var userRole, status string
		var role domain.ProjectRole
		if err := rows.Scan(&userRole, &status, &role); err != nil {
			return fmt.Errorf("读取项目成员失败：%w", err)
		}
		if status != UserStatusActive {
			continue
		}
		switch userRole {
		case UserRoleSuperAdmin:
			allowed = true
		case UserRoleAdmin, UserRoleUser:
			// 普通账户即使具有项目管理角色，也不能直接读取完整证书私钥。
			if permission == domain.PermissionPrivateKeyRead && userRole == UserRoleUser {
				continue
			}
			allowed = allowed || role.Allows(permission)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("读取项目权限结果失败：%w", err)
	}
	if !allowed {
		return ErrProjectAccessDenied
	}
	return nil
}

// 授权与写入在同一条语句内判定，避免先检查再写入之间撤权导致越权。
// 系统管理员必须是当前有效超级管理员；普通管理员不会因用户组或全局角色自动获得项目权限。
const projectManagerPredicate = `EXISTS (SELECT 1 FROM users a WHERE a.id = ? AND a.status = 'active'
	AND (a.role = 'super_admin' OR (a.role IN ('admin', 'user') AND EXISTS
		(SELECT 1 FROM project_members manager WHERE manager.project_id = ?
		AND manager.user_id = a.id AND manager.role = 'project_admin'))))`

func (r *SQLProjectMemberRepository) GrantProjectRole(ctx context.Context, actorID, projectID, userID string, role domain.ProjectRole) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	actorID, projectID, userID = strings.TrimSpace(actorID), strings.TrimSpace(projectID), strings.TrimSpace(userID)
	if actorID == "" || projectID == "" || userID == "" || !role.Valid() {
		return ErrProjectAccessDenied
	}
	result, err := r.db.ExecContext(ctx, Rebind(r.driver, `INSERT INTO project_members (project_id, user_id, role, created_at)
		SELECT p.id, u.id, ?, ? FROM projects p JOIN users u ON u.id = ? AND u.status = 'active'
		WHERE p.id = ? AND `+projectManagerPredicate), role, time.Now().UTC(), userID, projectID, actorID, projectID)
	if err != nil {
		return fmt.Errorf("授予项目角色失败：%w", err)
	}
	return projectMemberWriteResult(result)
}

func (r *SQLProjectMemberRepository) RevokeProjectRole(ctx context.Context, actorID, projectID, userID string, role domain.ProjectRole) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	actorID, projectID, userID = strings.TrimSpace(actorID), strings.TrimSpace(projectID), strings.TrimSpace(userID)
	if actorID == "" || projectID == "" || userID == "" || !role.Valid() {
		return ErrProjectAccessDenied
	}
	query := `DELETE FROM project_members WHERE project_id = ? AND user_id = ? AND role = ? AND ` + projectManagerPredicate
	if r.driver == "mysql" {
		// MySQL 不允许普通子查询读取正在删除的表，使用自连接完成同一语句内的判权。
		query = `DELETE target FROM project_members target
			JOIN users a ON a.id = ? AND a.status = 'active'
			LEFT JOIN project_members manager ON manager.project_id = target.project_id
			AND manager.user_id = a.id AND manager.role = 'project_admin'
			WHERE target.project_id = ? AND target.user_id = ? AND target.role = ?
			AND (a.role = 'super_admin' OR (a.role IN ('admin', 'user') AND manager.user_id IS NOT NULL))`
		result, err := r.db.ExecContext(ctx, query, actorID, projectID, userID, role)
		if err != nil {
			return fmt.Errorf("撤销项目角色失败：%w", err)
		}
		return projectMemberWriteResult(result)
	}
	result, err := r.db.ExecContext(ctx, Rebind(r.driver, query), projectID, userID, role, actorID, projectID)
	if err != nil {
		return fmt.Errorf("撤销项目角色失败：%w", err)
	}
	return projectMemberWriteResult(result)
}

func projectMemberWriteResult(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取项目成员变更结果失败：%w", err)
	}
	if count != 1 {
		return ErrProjectAccessDenied
	}
	return nil
}
