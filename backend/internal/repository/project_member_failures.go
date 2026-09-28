package repository

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ProjectMemberFailure 的操作者必须来自已验证会话快照；未登录时留空，不能从请求体读取。
type ProjectMemberFailure struct {
	ActorID    string
	ProjectID  string
	UserID     string
	Action     string
	StatusCode int
	RequestID  string
}

// 失败事件独立于已回滚的变更事务，范围由实际项目解析，不将不存在的项目伪装为系统范围。
func (r *SQLProjectMemberRepository) RecordProjectMemberFailure(ctx context.Context, failure ProjectMemberFailure) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	if failure.Action != "project_member.list" && failure.Action != "project_member.grant" && failure.Action != "project_member.revoke" {
		return errors.New("项目成员失败审计动作无效")
	}
	if failure.StatusCode < 400 || failure.StatusCode > 599 || len(failure.ActorID) > 128 || len(failure.UserID) > 128 || len(failure.ProjectID) > 128 || len(failure.RequestID) > 128 {
		return errors.New("项目成员失败审计参数无效")
	}
	message := fmt.Sprintf("请求被拒绝或执行失败：HTTP %d；请求编号：%s", failure.StatusCode, failure.RequestID)
	result, err := r.db.ExecContext(ctx, Rebind(r.driver, `INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		SELECT ?, 'project_member', ?, 'failed', ?, ?,
			CASE WHEN p.id IS NULL THEN 'unknown' ELSE 'project' END, COALESCE(p.id, ''), '', ?
		FROM (SELECT 1 AS marker) audit_event LEFT JOIN projects p ON p.id = ?`),
		failure.Action, failure.UserID, message, time.Now().UTC(), failure.ActorID, failure.ProjectID)
	if err != nil {
		return fmt.Errorf("保存项目成员失败审计失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return errors.New("项目成员失败审计未完整写入")
	}
	return nil
}
