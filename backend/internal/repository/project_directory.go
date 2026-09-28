package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ProjectDirectoryRepository 只返回当前有效账户有权读取的项目，不暴露基础仓储的全量列表。
type ProjectDirectoryRepository interface {
	ListVisibleProjects(context.Context, string, string, int) (VisibleProjectPage, error)
}

type VisibleProject struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	CanManageMembers bool   `json:"can_manage_members"`
	CanManageProject bool   `json:"can_manage_project"`
	CanDeleteProject bool   `json:"can_delete_project"`
}

type VisibleProjectPage struct {
	Items            []VisibleProject `json:"items"`
	PageSize         int              `json:"page_size"`
	HasMore          bool             `json:"has_more"`
	NextCursor       string           `json:"next_cursor"`
	CanCreateProject bool             `json:"can_create_project"`
}

var _ ProjectDirectoryRepository = (*SQLProjectMemberRepository)(nil)

func (r *SQLProjectMemberRepository) ListVisibleProjects(ctx context.Context, actorID, after string, limit int) (VisibleProjectPage, error) {
	page := VisibleProjectPage{Items: make([]VisibleProject, 0), PageSize: limit}
	if err := r.ensureDB(); err != nil {
		return page, err
	}
	if limit < 1 || limit > 100 || len(after) > 128 {
		return page, errors.New("项目分页参数无效")
	}
	// 授权、可见性过滤与读取共用一条 SQL；有效账户没有项目时仍返回授权门行。
	// 使用 EXISTS 避免一个账户的多个角色造成重复项目和错误分页。
	rows, err := r.db.QueryContext(ctx, Rebind(r.driver, `SELECT a.id, a.role, p.id, p.name, p.description,
		CASE WHEN a.role = 'super_admin' OR EXISTS (
			SELECT 1 FROM project_members manager WHERE manager.project_id = p.id
			AND manager.user_id = a.id AND manager.role = 'project_admin'
		) THEN 1 ELSE 0 END
		FROM users a LEFT JOIN projects p ON p.id > ? AND (
			a.role = 'super_admin' OR EXISTS (
				SELECT 1 FROM project_members member WHERE member.project_id = p.id AND member.user_id = a.id
				AND member.role IN ('project_admin', 'certificate_admin', 'deployment_admin', 'dns_admin', 'audit_viewer', 'read_only')
			)
		)
		WHERE a.id = ? AND a.status = 'active' AND a.role IN ('super_admin', 'admin', 'user')
		ORDER BY p.id LIMIT ?`), after, actorID, limit+1)
	if err != nil {
		return page, fmt.Errorf("查询可见项目失败：%w", err)
	}
	defer rows.Close()
	allowed := false
	for rows.Next() {
		var actor, globalRole string
		var id, name, description sql.NullString
		var manager int
		if err := rows.Scan(&actor, &globalRole, &id, &name, &description, &manager); err != nil {
			return page, fmt.Errorf("读取可见项目失败：%w", err)
		}
		allowed = true
		page.CanCreateProject = globalRole == UserRoleSuperAdmin
		if id.Valid {
			page.Items = append(page.Items, VisibleProject{
				ID: id.String, Name: name.String, Description: description.String, CanManageMembers: manager == 1,
				CanManageProject: manager == 1, CanDeleteProject: manager == 1 && id.String != "default",
			})
		}
	}
	if err := rows.Err(); err != nil {
		return VisibleProjectPage{}, fmt.Errorf("读取可见项目结果失败：%w", err)
	}
	if !allowed {
		return page, ErrProjectAccessDenied
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.HasMore = true
		page.NextCursor = page.Items[limit-1].ID
	}
	return page, nil
}
