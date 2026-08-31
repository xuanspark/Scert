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
	Action       string
	ResourceType string
	ResourceID   string
	Status       string
	Message      string
	CreatedAt    time.Time
}

type AuditRepository interface {
	CreateAuditLog(ctx context.Context, item AuditLog) error
}

type SQLAuditRepository struct {
	db *sql.DB
}

func NewSQLAuditRepository(db *sql.DB) *SQLAuditRepository {
	return &SQLAuditRepository{db: db}
}

func (r *SQLAuditRepository) CreateAuditLog(ctx context.Context, item AuditLog) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	item.Action = strings.TrimSpace(item.Action)
	item.ResourceType = strings.TrimSpace(item.ResourceType)
	item.ResourceID = strings.TrimSpace(item.ResourceID)
	item.Status = strings.TrimSpace(item.Status)
	if item.Action == "" || item.ResourceType == "" || item.Status == "" {
		return errors.New("审计日志必要字段不能为空")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, item.Action, item.ResourceType, item.ResourceID, item.Status, item.Message, item.CreatedAt.UTC())
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
	result, err := r.db.ExecContext(ctx, `DELETE FROM audit_logs WHERE created_at < ?`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("清理过期审计日志失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取审计日志清理数量失败：%w", err)
	}
	return count, nil
}
