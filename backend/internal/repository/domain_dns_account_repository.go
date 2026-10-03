package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

var (
	ErrDNSBindingConflict = errors.New("域名 DNS 账户绑定已变更，请刷新后重试")
	ErrDNSBindingBusy     = errors.New("域名存在未结束的任务，暂不能变更 DNS 账户")
	ErrDNSBindingInvalid  = errors.New("域名验证方式或 DNS 账户状态不允许绑定")
)

type DomainDNSAccountRepository interface {
	GetDomainDNSAccountBinding(context.Context, string, string) (domain.DNSAccountBinding, error)
	SetDomainDNSAccountBinding(context.Context, string, string, string, string, int64) (domain.DNSAccountBinding, error)
}

var _ DomainDNSAccountRepository = (*SQLProjectMemberRepository)(nil)

func (r *SQLProjectMemberRepository) GetDomainDNSAccountBinding(ctx context.Context, actorID, domainID string) (domain.DNSAccountBinding, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return domain.DNSAccountBinding{}, err
	}
	predicate, args := resourceReadPredicate("domains.project_id", actorID, domain.PermissionCertificateRead)
	args = append(args, domainID)
	var result domain.DNSAccountBinding
	var id, accountID sql.NullString
	var updated sql.NullTime
	err := r.db.QueryRowContext(ctx, Rebind(r.driver, `SELECT domains.id, domains.project_id, binding.id, binding.dns_account_id, binding.updated_at
		FROM domains LEFT JOIN domain_dns_accounts binding ON binding.domain_id = domains.id AND binding.project_id = domains.project_id
		WHERE `+predicate+` AND domains.id = ?`), args...).Scan(&result.DomainID, &result.ProjectID, &id, &accountID, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DNSAccountBinding{}, ErrProjectAccessDenied
	}
	if err != nil {
		return domain.DNSAccountBinding{}, err
	}
	result.ID, result.DNSAccountID, result.UpdatedAt = id.String, accountID.String, updated.Time
	return result, nil
}

// 空账户表示显式解除；expectedID 为空只允许首次绑定。凭据版本和旧绑定同时参与校验。
// 这里只变更配置，不授予签发权限；执行阶段必须再次获得实际凭据租约。
func (r *SQLProjectMemberRepository) SetDomainDNSAccountBinding(ctx context.Context, actorID, domainID, accountID, expectedID string, accountRevision int64) (domain.DNSAccountBinding, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return domain.DNSAccountBinding{}, err
	}
	if !domain.ValidProjectID(domainID) || (accountID != "" && (!domain.ValidProjectID(accountID) || accountRevision < 1)) || (expectedID != "" && !domain.ValidProjectID(expectedID)) {
		return domain.DNSAccountBinding{}, ErrDNSBindingInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.DNSAccountBinding{}, err
	}
	defer tx.Rollback()
	var result domain.DNSAccountBinding
	result.DomainID = domainID
	var challengeType string
	if err := tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT project_id, challenge_type FROM domains WHERE id = ?`+r.resourceWriteLock()), domainID).Scan(&result.ProjectID, &challengeType); err != nil {
		return domain.DNSAccountBinding{}, projectBindingReadError(err)
	}
	permissions := []domain.ProjectPermission{domain.PermissionCertificateManage}
	if accountID != "" {
		permissions = append(permissions, domain.PermissionDNSCredentialUse)
	}
	if err := r.authorizeProjectTransaction(ctx, tx, actorID, result.ProjectID, permissions...); err != nil {
		return domain.DNSAccountBinding{}, err
	}
	var currentID string
	err = tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT id FROM domain_dns_accounts WHERE domain_id = ?`+r.resourceWriteLock()), domainID).Scan(&currentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.DNSAccountBinding{}, err
	}
	if currentID != expectedID {
		return domain.DNSAccountBinding{}, ErrDNSBindingConflict
	}
	// 与任务创建持有同一域名行锁，避免检查后插入新任务并使用旧绑定。
	var activeID string
	err = tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT id FROM tasks WHERE domain_id = ?
		AND status IN ('queued', 'running', 'retry_wait', 'cancelling') LIMIT 1`+r.resourceWriteLock()), domainID).Scan(&activeID)
	if err == nil {
		return domain.DNSAccountBinding{}, ErrDNSBindingBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.DNSAccountBinding{}, err
	}
	provider := ""
	if accountID != "" {
		if challengeType != domain.ChallengeDNS01 {
			return domain.DNSAccountBinding{}, ErrDNSBindingInvalid
		}
		var projectID, status, verification string
		var revision int64
		if err := tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT project_id, provider, status, verification_state, revision
			FROM dns_accounts WHERE id = ?`+r.resourceWriteLock()), accountID).Scan(&projectID, &provider, &status, &verification, &revision); err != nil {
			return domain.DNSAccountBinding{}, projectBindingReadError(err)
		}
		if projectID != result.ProjectID {
			return domain.DNSAccountBinding{}, ErrProjectAccessDenied
		}
		if revision != accountRevision {
			return domain.DNSAccountBinding{}, ErrDNSAccountConflict
		}
		if status != domain.DNSAccountActive || verification != domain.DNSAccountVerified {
			return domain.DNSAccountBinding{}, ErrDNSBindingInvalid
		}
		result.ID, result.DNSAccountID = domain.NewID(), accountID
	}
	result.UpdatedAt = time.Now().UTC()
	if _, err := tx.ExecContext(ctx, Rebind(r.driver, `DELETE FROM domain_dns_accounts WHERE domain_id = ?`), domainID); err != nil {
		return domain.DNSAccountBinding{}, err
	}
	if accountID != "" {
		if _, err := tx.ExecContext(ctx, Rebind(r.driver, `INSERT INTO domain_dns_accounts (domain_id, id, project_id, dns_account_id, updated_at)
			VALUES (?, ?, ?, ?, ?)`), domainID, result.ID, result.ProjectID, accountID, result.UpdatedAt); err != nil {
			return domain.DNSAccountBinding{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, Rebind(r.driver, `UPDATE domains SET dns_provider = ?, updated_at = ?, updated_by = ? WHERE id = ?`), provider, result.UpdatedAt, actorID, domainID); err != nil {
		return domain.DNSAccountBinding{}, err
	}
	if err := writeDomainAudit(ctx, tx, r.driver, "domain.dns_account", domainID, result.ProjectID, actorID, "更新域名 DNS 账户绑定"); err != nil {
		return domain.DNSAccountBinding{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.DNSAccountBinding{}, err
	}
	return result, nil
}

// 普通域名编辑不能绕过独立绑定修改服务商或切换验证方式；须先显式解除绑定。
func validateBoundDomainDNS(ctx context.Context, tx *sql.Tx, driver string, item domain.Domain, challengeType string) error {
	var provider string
	err := tx.QueryRowContext(ctx, Rebind(driver, `SELECT account.provider FROM domain_dns_accounts binding
		JOIN dns_accounts account ON account.id = binding.dns_account_id AND account.project_id = binding.project_id
		WHERE binding.domain_id = ?`), item.ID).Scan(&provider)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if challengeType != domain.ChallengeDNS01 || item.DNSProvider != provider {
		return ErrDNSBindingInvalid
	}
	return nil
}
