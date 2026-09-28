package repository

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// 失败范围由实际域名解析；不存在的资源保留未知范围，不相信请求中的项目或操作者。
func (r *SQLProjectMemberRepository) RecordProjectBindingFailure(ctx context.Context, failure ProjectBindingFailure) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	if failure.StatusCode < 400 || failure.StatusCode > 599 || len(failure.ActorID) > 128 || len(failure.DomainID) > 128 || len(failure.RequestID) > 128 {
		return errors.New("项目绑定失败审计参数无效")
	}
	result, err := r.db.ExecContext(ctx, Rebind(r.driver, `INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		SELECT 'domain.servers.replace', 'domain', ?, 'failed', ?, ?,
			CASE WHEN d.id IS NULL THEN 'unknown' ELSE 'project' END, COALESCE(d.project_id, ''), '', ?
		FROM (SELECT 1 AS marker) audit_event LEFT JOIN domains d ON d.id = ?`),
		failure.DomainID, fmt.Sprintf("绑定请求被拒绝或执行失败：HTTP %d；请求编号：%s", failure.StatusCode, failure.RequestID),
		time.Now().UTC(), failure.ActorID, failure.DomainID)
	if err != nil {
		return fmt.Errorf("保存项目绑定失败审计失败：%w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errors.New("项目绑定失败审计未写入")
	}
	return nil
}
