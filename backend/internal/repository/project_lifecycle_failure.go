package repository

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type ProjectFailure struct {
	ActorID    string
	ProjectID  string
	Action     string
	StatusCode int
	RequestID  string
}

// 失败记录只保留已验证身份、实际范围和安全诊断，不包含请求正文或数据库原始错误。
func (r *SQLProjectMemberRepository) RecordProjectFailure(ctx context.Context, failure ProjectFailure) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	if failure.Action != "project.create" && failure.Action != "project.update" && failure.Action != "project.delete" {
		return errors.New("项目失败审计动作无效")
	}
	if failure.StatusCode < 400 || failure.StatusCode > 599 || len(failure.ActorID) > 128 || len(failure.ProjectID) > 128 || len(failure.RequestID) > 128 {
		return errors.New("项目失败审计参数无效")
	}
	result, err := r.db.ExecContext(ctx, Rebind(r.driver, `INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		SELECT ?, 'project', ?, 'failed', ?, ?, CASE WHEN p.id IS NULL THEN 'unknown' ELSE 'project' END,
		COALESCE(p.id, ''), '', ? FROM (SELECT 1 AS marker) audit_event LEFT JOIN projects p ON p.id = ?`),
		failure.Action, failure.ProjectID, fmt.Sprintf("请求被拒绝或执行失败：HTTP %d；请求编号：%s", failure.StatusCode, failure.RequestID),
		time.Now().UTC(), failure.ActorID, failure.ProjectID)
	if err != nil {
		return fmt.Errorf("保存项目失败审计失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return errors.New("项目失败审计未写入")
	}
	return nil
}
