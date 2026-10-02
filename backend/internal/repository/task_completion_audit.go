package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// 完成审计从已锁定的任务取资源、操作者及项目，不信任外部传入的归属字段。
func (r *SQLTaskRepository) recordCompletionAudit(ctx context.Context, tx *sql.Tx, completion TaskCompletion) error {
	status := completion.Status
	if status == "failed" {
		status = "error"
	}
	result, err := tx.ExecContext(ctx, r.query(`INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		SELECT ?, CASE WHEN task_type = 'renewal_check' THEN 'task' WHEN scope_type = 'system' THEN 'management_certificate' ELSE 'domain' END,
			CASE WHEN task_type = 'renewal_check' THEN id ELSE domain_id END, ?, ?, ?, scope_type, project_id, id, operator_id
		FROM tasks WHERE id = ?`), completion.AuditAction, status, completion.Message,
		completion.FinishedAt.UTC(), completion.TaskID)
	if err != nil {
		return fmt.Errorf("保存任务完成审计失败：%w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("读取任务完成审计写入结果失败：%w", err)
	} else if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
