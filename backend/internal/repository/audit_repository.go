package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type AuditLog struct {
	HistoryScope
	TaskID       string    `json:"task_id"`
	OperatorID   string    `json:"operator_id"`
	ID           int64     `json:"id"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Status       string    `json:"status"`
	Message      string    `json:"message"`
	CreatedAt    time.Time `json:"created_at"`
}

type AuditRepository interface {
	CreateAuditLog(ctx context.Context, item AuditLog) error
}

type SQLAuditRepository struct {
	db     *sql.DB
	driver string
}

func NewSQLAuditRepository(db *sql.DB, driver ...string) *SQLAuditRepository {
	return &SQLAuditRepository{db: db, driver: repositoryDriver(driver)}
}

func (r *SQLAuditRepository) query(value string) string {
	return Rebind(r.driver, value)
}

func (r *SQLAuditRepository) CreateAuditLog(ctx context.Context, item AuditLog) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	item.Action = strings.TrimSpace(item.Action)
	item.ResourceType = strings.TrimSpace(item.ResourceType)
	item.ResourceID = strings.TrimSpace(item.ResourceID)
	item.Status = strings.TrimSpace(item.Status)
	item.TaskID = strings.TrimSpace(item.TaskID)
	item.OperatorID = strings.TrimSpace(item.OperatorID)
	if item.Action == "" || item.ResourceType == "" || item.Status == "" {
		return errors.New("审计日志必要字段不能为空")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.ResourceType == "task" && item.TaskID == "" {
		item.TaskID = item.ResourceID
	}
	scope, err := r.resolveScope(ctx, item)
	if err != nil {
		return fmt.Errorf("解析审计范围失败：%w", err)
	}
	if !item.HistoryScope.matches(scope) {
		return errors.New("审计项目与资源范围不匹配")
	}
	_, err = r.db.ExecContext(ctx, r.query(`INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), item.Action, item.ResourceType, item.ResourceID, item.Status, item.Message,
		item.CreatedAt.UTC(), scope.ScopeType, scope.ProjectID, item.TaskID, item.OperatorID)
	if err != nil {
		return fmt.Errorf("保存审计日志失败：%w", err)
	}
	return nil
}

func (r *SQLAuditRepository) PruneExpiredAuditLogs(ctx context.Context, before time.Time) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("数据库连接不能为空")
	}
	if before.IsZero() {
		return 0, errors.New("审计日志清理时间不能为空")
	}
	result, err := r.db.ExecContext(ctx, r.query(`DELETE FROM audit_logs WHERE created_at < ?`), before.UTC())
	if err != nil {
		return 0, fmt.Errorf("清理过期审计日志失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取审计日志清理数量失败：%w", err)
	}
	return count, nil
}
