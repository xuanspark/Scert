package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type SQLDomainRepository struct {
	db     *sql.DB
	driver string
}

func NewSQLDomainRepository(db *sql.DB, driver ...string) *SQLDomainRepository {
	return &SQLDomainRepository{db: db, driver: repositoryDriver(driver)}
}

func (r *SQLDomainRepository) query(value string) string {
	return Rebind(r.driver, value)
}

func (r *SQLDomainRepository) Create(ctx context.Context, item domain.Domain) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	payload, err := json.Marshal(item.SANs)
	if err != nil {
		return fmt.Errorf("编码 SAN 域名失败：%w", err)
	}
	challengeType, err := domain.NormalizeChallengeType(item.ChallengeType, append([]string{item.Name}, item.SANs...))
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, r.query(`INSERT INTO domains
		(id, name, certificate_type, sans_json, auto_renew, acme_environment, dns_provider, status, expires_at, created_at, updated_at, project_id, owner_id, created_by, updated_by, challenge_type)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), item.ID, item.Name, item.CertificateType, string(payload), item.AutoRenew,
		item.ACMEEnvironment, item.DNSProvider, item.Status, nullableTime(item.ExpiresAt), item.CreatedAt, item.UpdatedAt,
		item.ResourceScope.Normalize().ProjectID, item.OwnerID, item.CreatedBy, item.UpdatedBy, challengeType)
	if err != nil {
		return fmt.Errorf("保存域名失败：%w", err)
	}
	return nil
}

func (r *SQLDomainRepository) Update(ctx context.Context, item domain.Domain) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	payload, err := json.Marshal(item.SANs)
	if err != nil {
		return fmt.Errorf("编码 SAN 域名失败：%w", err)
	}
	challengeType, err := domain.NormalizeChallengeType(item.ChallengeType, append([]string{item.Name}, item.SANs...))
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var storedID string
	if err := tx.QueryRowContext(ctx, r.query(`SELECT id FROM domains WHERE id = ? AND project_id = ?`+taskResourceWriteLock(r.driver)), item.ID, item.ResourceScope.Normalize().ProjectID).Scan(&storedID); err != nil {
		return err
	}
	if err := validateBoundDomainDNS(ctx, tx, r.driver, item, challengeType); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, r.query(`UPDATE domains SET
		name = ?, certificate_type = ?, sans_json = ?, auto_renew = ?, acme_environment = ?, dns_provider = ?, updated_at = ?, updated_by = ?, challenge_type = ?
		WHERE id = ? AND project_id = ?`), item.Name, item.CertificateType, string(payload), item.AutoRenew, item.ACMEEnvironment,
		item.DNSProvider, item.UpdatedAt, item.UpdatedBy, challengeType, item.ID, item.ResourceScope.Normalize().ProjectID)
	if err != nil {
		return fmt.Errorf("更新域名失败：%w", err)
	}
	return tx.Commit()
}

func (r *SQLDomainRepository) GetByID(ctx context.Context, id string) (domain.Domain, error) {
	if r == nil || r.db == nil {
		return domain.Domain{}, errors.New("数据库连接不能为空")
	}
	row := r.db.QueryRowContext(ctx, r.query(visibleDomainSelect+` WHERE domains.id = ?`), id)
	return scanDomain(row)
}

func (r *SQLDomainRepository) List(ctx context.Context) ([]domain.Domain, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	rows, err := r.db.QueryContext(ctx, r.query(visibleDomainSelect+` ORDER BY domains.name`))
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始删除域名失败：%w", err)
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	if _, err := tx.ExecContext(ctx, r.query("DELETE FROM domain_servers WHERE domain_id = ?"), id); err != nil {
		return rollback(fmt.Errorf("删除域名服务器关联失败：%w", err))
	}
	if _, err := tx.ExecContext(ctx, r.query("DELETE FROM domain_dns_accounts WHERE domain_id = ?"), id); err != nil {
		return rollback(err)
	}
	result, err := tx.ExecContext(ctx, r.query("DELETE FROM domains WHERE id = ?"), id)
	if err != nil {
		return rollback(fmt.Errorf("删除域名失败：%w", err))
	}
	count, err := result.RowsAffected()
	if err != nil {
		return rollback(fmt.Errorf("读取删除结果失败：%w", err))
	}
	if count == 0 {
		return rollback(sql.ErrNoRows)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交域名删除失败：%w", err)
	}
	return nil
}

func (r *SQLDomainRepository) UpdateCertificateState(ctx context.Context, id, status string, expiresAt *time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return errors.New("证书状态不能为空")
	}
	result, err := r.db.ExecContext(ctx, r.query(`UPDATE domains SET status = ?, expires_at = ?, updated_at = ? WHERE id = ?`),
		status, nullableTime(expiresAt), time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("更新证书状态失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取证书状态更新结果失败：%w", err)
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
	var autoRenew bool
	var acmeEnvironment string
	var dnsProvider string
	var expiresAt sql.NullTime
	if err := row.Scan(&item.ID, &item.Name, &certificateType, &sansJSON, &autoRenew, &acmeEnvironment, &dnsProvider, &item.Status, &expiresAt, &item.CreatedAt, &item.UpdatedAt, &item.ProjectID, &item.OwnerID, &item.CreatedBy, &item.UpdatedBy, &item.ChallengeType, &item.DNSAccountID, &item.DNSBindingID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Domain{}, sql.ErrNoRows
		}
		return domain.Domain{}, fmt.Errorf("读取域名失败：%w", err)
	}
	if err := json.Unmarshal([]byte(sansJSON), &item.SANs); err != nil {
		return domain.Domain{}, fmt.Errorf("解析 SAN 域名失败：%w", err)
	}
	item.CertificateType = domain.CertificateType(certificateType)
	item.AutoRenew = autoRenew
	item.ACMEEnvironment = acmeEnvironment
	item.DNSProvider = dnsProvider
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

func IsConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate") ||
		strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "constraint failed")
}
