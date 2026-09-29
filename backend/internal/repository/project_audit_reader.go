package repository

import (
	"context"
	"fmt"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// ProjectAuditReader 只返回当前账户有权查看的项目审计记录。
type ProjectAuditReader interface {
	ListVisibleAuditLogs(context.Context, string, int) ([]AuditLog, error)
}

var _ ProjectAuditReader = (*SQLProjectMemberRepository)(nil)

func (r *SQLProjectMemberRepository) ListVisibleAuditLogs(ctx context.Context, actorID string, limit int) ([]AuditLog, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, Rebind(r.driver, `SELECT logs.id, logs.action,
		logs.resource_type, logs.resource_id, logs.status, logs.message, logs.created_at,
		logs.scope_type, logs.project_id, logs.task_id, logs.operator_id
		FROM audit_logs logs WHERE EXISTS (
			SELECT 1 FROM users actor WHERE actor.id = ? AND actor.status = 'active'
			AND (actor.role IN ('super_admin', 'admin') OR (actor.role = 'user'
				AND logs.scope_type = 'project' AND EXISTS (
					SELECT 1 FROM project_members member
					WHERE member.project_id = logs.project_id AND member.user_id = actor.id
					AND member.role IN (?, ?)))))
		ORDER BY logs.created_at DESC, logs.id DESC LIMIT ?`),
		actorID, string(domain.ProjectRoleAdmin), string(domain.ProjectRoleAuditViewer), limit)
	if err != nil {
		return nil, fmt.Errorf("查询项目审计日志失败：%w", err)
	}
	defer rows.Close()
	items := make([]AuditLog, 0)
	for rows.Next() {
		var item AuditLog
		if err := rows.Scan(&item.ID, &item.Action, &item.ResourceType, &item.ResourceID,
			&item.Status, &item.Message, &item.CreatedAt, &item.ScopeType, &item.ProjectID,
			&item.TaskID, &item.OperatorID); err != nil {
			return nil, fmt.Errorf("读取项目审计日志失败：%w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取项目审计日志列表失败：%w", err)
	}
	return items, nil
}
