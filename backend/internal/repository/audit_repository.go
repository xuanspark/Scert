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
