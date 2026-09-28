package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// ProjectMemberRepository 不接受客户端声明的操作者角色，所有操作均重新核对实际账户与成员关系。
type ProjectMemberRepository interface {
	ListProjectMembers(context.Context, string, string, string, int) (ProjectMemberPage, error)
	GrantProjectRole(context.Context, string, string, string, domain.ProjectRole) error
	RevokeProjectRole(context.Context, string, string, string, domain.ProjectRole) error
	RecordProjectMemberFailure(context.Context, ProjectMemberFailure) error
}

var _ ProjectMemberRepository = (*SQLProjectMemberRepository)(nil)

type ProjectMember struct {
	UserID   string               `json:"user_id"`
	Username string               `json:"username"`
	Status   string               `json:"status"`
	Roles    []domain.ProjectRole `json:"roles"`
}

type ProjectMemberPage struct {
	Items      []ProjectMember `json:"items"`
	PageSize   int             `json:"page_size"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor"`
}

// ListProjectMembers 使用成员账户而非角色关系分页，同一账户的角色不会被拆到不同页。
// 授权门与分页读取共用一条 SQL，空列表仍返回授权门行，未授权或项目不存在则没有行。
func (r *SQLProjectMemberRepository) ListProjectMembers(ctx context.Context, actorID, projectID, after string, limit int) (ProjectMemberPage, error) {
	page := ProjectMemberPage{Items: make([]ProjectMember, 0), PageSize: limit}
	if err := r.ensureDB(); err != nil {
		return page, err
	}
	if limit < 1 || limit > 100 || len(after) > 128 {
		return page, errors.New("项目成员分页参数无效")
	}
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(projectID) == "" {
		return page, ErrProjectAccessDenied
	}
	rows, err := r.db.QueryContext(ctx, Rebind(r.driver, `SELECT p.id, member_page.id, member_page.username, member_page.status, roles.role
		FROM projects p
		LEFT JOIN (
			SELECT u.id, u.username, u.status FROM users u
			WHERE u.id > ? AND EXISTS (SELECT 1 FROM project_members selected
				WHERE selected.project_id = ? AND selected.user_id = u.id)
			ORDER BY u.id LIMIT ?
		) member_page ON 1 = 1
		LEFT JOIN project_members roles ON roles.project_id = p.id AND roles.user_id = member_page.id
		WHERE p.id = ? AND `+projectManagerPredicate+`
		ORDER BY member_page.id, roles.role`), after, projectID, limit+1, projectID, actorID, projectID)
	if err != nil {
		return page, fmt.Errorf("查询项目成员失败：%w", err)
	}
	defer rows.Close()
	allowed := false
	for rows.Next() {
		var actualProject string
		var id, username, status, role sql.NullString
		if err := rows.Scan(&actualProject, &id, &username, &status, &role); err != nil {
			return page, fmt.Errorf("读取项目成员失败：%w", err)
		}
		allowed = true
		if !id.Valid {
			continue
		}
		if !role.Valid || !domain.ProjectRole(role.String).Valid() {
			return ProjectMemberPage{}, errors.New("项目成员角色数据无效")
		}
		if len(page.Items) == 0 || page.Items[len(page.Items)-1].UserID != id.String {
			page.Items = append(page.Items, ProjectMember{
				UserID: id.String, Username: username.String, Status: status.String,
				Roles: make([]domain.ProjectRole, 0, 6),
			})
		}
		last := &page.Items[len(page.Items)-1]
		last.Roles = append(last.Roles, domain.ProjectRole(role.String))
	}
	if err := rows.Err(); err != nil {
		return ProjectMemberPage{}, fmt.Errorf("读取项目成员结果失败：%w", err)
	}
	if !allowed {
		return page, ErrProjectAccessDenied
	}
	if len(page.Items) > limit {
		page.HasMore = true
		page.Items = page.Items[:limit]
		page.NextCursor = page.Items[limit-1].UserID
	}
	return page, nil
}
