package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type SQLDomainRepository struct {
	db *sql.DB
}

func NewSQLDomainRepository(db *sql.DB) *SQLDomainRepository {
	return &SQLDomainRepository{db: db}
}

func (r *SQLDomainRepository) Create(ctx context.Context, item domain.Domain) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	payload, err := json.Marshal(item.SANs)
	if err != nil {
		return fmt.Errorf("编码 SAN 域名失败：%w", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO domains
		(id, name, certificate_type, sans_json, status, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, item.Name, item.CertificateType, string(payload), item.Status,
		nullableTime(item.ExpiresAt), item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return fmt.Errorf("保存域名失败：%w", err)
	}
	return nil
}

func (r *SQLDomainRepository) GetByID(ctx context.Context, id string) (domain.Domain, error) {
	if r == nil || r.db == nil {
		return domain.Domain{}, errors.New("数据库连接不能为空")
	}
	row := r.db.QueryRowContext(ctx, `SELECT id, name, certificate_type, sans_json, status, expires_at, created_at, updated_at
		FROM domains WHERE id = ?`, id)
	return scanDomain(row)
}

func (r *SQLDomainRepository) List(ctx context.Context) ([]domain.Domain, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, certificate_type, sans_json, status, expires_at, created_at, updated_at
		FROM domains ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("查询域名失败：%w", err)
	}
	defer rows.Close()
	items := make([]domain.Domain, 0)
	for rows.Next() {
		item, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取域名结果失败：%w", err)
	}
	return items, nil
}

func (r *SQLDomainRepository) Delete(ctx context.Context, id string) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	result, err := r.db.ExecContext(ctx, "DELETE FROM domains WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("删除域名失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取删除结果失败：%w", err)
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDomain(row scanner) (domain.Domain, error) {
	var item domain.Domain
	var certificateType string
	var sansJSON string
	var expiresAt sql.NullTime
	if err := row.Scan(&item.ID, &item.Name, &certificateType, &sansJSON, &item.Status, &expiresAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Domain{}, sql.ErrNoRows
		}
		return domain.Domain{}, fmt.Errorf("读取域名失败：%w", err)
	}
	if err := json.Unmarshal([]byte(sansJSON), &item.SANs); err != nil {
		return domain.Domain{}, fmt.Errorf("解析 SAN 域名失败：%w", err)
	}
	item.CertificateType = domain.CertificateType(certificateType)
	if expiresAt.Valid {
		value := expiresAt.Time
		item.ExpiresAt = &value
	}
	return item, nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func IsNotFound(err error) bool {
	return err != nil && errors.Is(err, sql.ErrNoRows)
}
