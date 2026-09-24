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
	if item.Action == "" || item.ResourceType == "" || item.Status == "" {
		return errors.New("审计日志必要字段不能为空")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, r.query(`INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`), item.Action, item.ResourceType, item.ResourceID, item.Status, item.Message, item.CreatedAt.UTC())
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
