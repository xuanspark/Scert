package database

import (
	"context"
	"database/sql"
	"fmt"
)

// 历史范围是不可级联删除的快照，不强行给已删除资源补默认项目或操作者。
// MySQL 的部分 DDL 重试只回填仍未知的记录，不覆盖已经保存的范围。
func backfillHistoryScope(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) error {
	statements := []string{
		`UPDATE tasks SET scope_type = 'system' WHERE scope_type = 'unknown'
			AND task_type = 'management_renewal' AND domain_id = 'management-certificate'`,
		`UPDATE tasks SET scope_type = 'project',
			project_id = (SELECT d.project_id FROM domains d WHERE d.id = tasks.domain_id)
			WHERE scope_type = 'unknown' AND EXISTS (SELECT 1 FROM domains d WHERE d.id = tasks.domain_id)`,
		`UPDATE audit_logs SET scope_type = 'system' WHERE scope_type = 'unknown'
			AND (resource_type IN ('auth', 'user', 'super_admin')
			OR (resource_type = 'management_certificate' AND resource_id = 'management-certificate'))`,
		`UPDATE audit_logs SET task_id = resource_id WHERE resource_type = 'task' AND task_id = ''`,
		`UPDATE audit_logs SET scope_type = (SELECT t.scope_type FROM tasks t WHERE t.id = audit_logs.task_id),
			project_id = (SELECT t.project_id FROM tasks t WHERE t.id = audit_logs.task_id)
			WHERE scope_type = 'unknown' AND task_id <> '' AND EXISTS (SELECT 1 FROM tasks t WHERE t.id = audit_logs.task_id)`,
		`UPDATE audit_logs SET scope_type = 'project',
			project_id = (SELECT d.project_id FROM domains d WHERE d.id = audit_logs.resource_id)
			WHERE scope_type = 'unknown' AND resource_type IN ('domain', 'certificate')
			AND EXISTS (SELECT 1 FROM domains d WHERE d.id = audit_logs.resource_id)`,
		`UPDATE audit_logs SET scope_type = 'project',
			project_id = (SELECT s.project_id FROM servers s WHERE s.id = audit_logs.resource_id)
			WHERE scope_type = 'unknown' AND resource_type = 'server'
			AND EXISTS (SELECT 1 FROM servers s WHERE s.id = audit_logs.resource_id)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("回填任务审计历史范围失败：%w", err)
		}
	}
	return nil
}
