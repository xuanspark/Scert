package repository

import (
	"context"
	"database/sql"
	"errors"
)

const (
	ScopeProject         = "project"
	ScopeSystem          = "system"
	ScopeUnknown         = "unknown"
	ManagementResourceID = "management-certificate"
)

// HistoryScope 保存事件发生时的范围，资源或项目删除后仍保留；不是项目授权凭证。
// 未知历史归属保持为空，不自动降级为系统范围，也不归入默认项目。
type HistoryScope struct {
	ScopeType string `json:"scope_type"`
	ProjectID string `json:"project_id"`
}

func (s HistoryScope) matches(actual HistoryScope) bool {
	return (s.ScopeType == "" || s.ScopeType == actual.ScopeType) && (s.ProjectID == "" || s.ProjectID == actual.ProjectID)
}

func (r *SQLAuditRepository) resolveScope(ctx context.Context, item AuditLog) (HistoryScope, error) {
	if item.TaskID != "" {
		task, err := NewSQLTaskRepository(r.db, r.driver).GetTask(ctx, item.TaskID)
		if err != nil {
			return HistoryScope{}, err
		}
		valid := item.ResourceType == "task" && item.ResourceID == task.ID
		if item.ResourceType == "domain" || item.ResourceType == "certificate" {
			valid = task.ScopeType != ScopeSystem && item.ResourceID == task.DomainID
		}
		if item.ResourceType == "management_certificate" {
			valid = task.ScopeType == ScopeSystem && item.ResourceID == ManagementResourceID
		}
		if !valid {
			return HistoryScope{}, errors.New("审计任务与资源不匹配")
		}
		return task.HistoryScope, nil
	}
	var table string
	switch item.ResourceType {
	case "auth", "user", "super_admin":
		return HistoryScope{ScopeType: ScopeSystem}, nil
	case "management_certificate":
		if item.ResourceID == ManagementResourceID {
			return HistoryScope{ScopeType: ScopeSystem}, nil
		}
	case "domain", "certificate":
		table = "domains"
	case "server":
		table = "servers"
	}
	result := HistoryScope{ScopeType: ScopeUnknown}
	if table != "" {
		err := r.db.QueryRowContext(ctx, r.query("SELECT project_id FROM "+table+" WHERE id = ?"), item.ResourceID).Scan(&result.ProjectID)
		if errors.Is(err, sql.ErrNoRows) {
			// 对不存在资源的失败操作也需要审计，但不能伪造其项目。
			return result, nil
		}
		if err != nil {
			return HistoryScope{}, err
		}
		result.ScopeType = ScopeProject
	}
	return result, nil
}
