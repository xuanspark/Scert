package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// AuditReader 用于读取操作审计记录，不改变写入接口的兼容性。
type AuditReader interface {
	ListAuditLogs(context.Context, int) ([]AuditLog, error)
}

func (r *SQLAuditRepository) ListAuditLogs(ctx context.Context, limit int) ([]AuditLog, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("数据库连接不能为空")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, r.query(`SELECT id, action, resource_type, resource_id, status, message, created_at,
		scope_type, project_id, task_id, operator_id FROM audit_logs ORDER BY created_at DESC, id DESC LIMIT `+fmt.Sprintf("%d", limit)))
	if err != nil {
		return nil, fmt.Errorf("查询审计日志失败：%w", err)
	}
	defer rows.Close()
	logs := make([]AuditLog, 0)
	for rows.Next() {
		var item AuditLog
		if err := rows.Scan(&item.ID, &item.Action, &item.ResourceType, &item.ResourceID, &item.Status, &item.Message, &item.CreatedAt,
			&item.ScopeType, &item.ProjectID, &item.TaskID, &item.OperatorID); err != nil {
			return nil, fmt.Errorf("读取审计日志失败：%w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		logs = append(logs, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取审计日志列表失败：%w", err)
	}
	return logs, nil
}

var _ AuditReader = (*SQLAuditRepository)(nil)
var _ = sql.ErrNoRows
