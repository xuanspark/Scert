package repository

import (
	"context"
	"database/sql"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// 创建域名与初始 DNS 绑定共用一个事务，自动续期看不到未绑定的中间状态。
func (r *SQLProjectMemberRepository) validateInitialDNSBinding(ctx context.Context, tx *sql.Tx, actorID string, item domain.Domain, challengeType string) error {
	if item.DNSAccountID == "" {
		if item.DNSBindingID != "" || item.InitialDNSRevision != 0 {
			return ErrDNSBindingInvalid
		}
		return nil
	}
	if !domain.ValidProjectID(item.DNSAccountID) || !domain.ValidProjectID(item.DNSBindingID) || item.InitialDNSRevision < 1 || challengeType != domain.ChallengeDNS01 {
		return ErrDNSBindingInvalid
	}
	if err := r.authorizeProjectTransaction(ctx, tx, actorID, item.ProjectID, domain.PermissionDNSCredentialUse); err != nil {
		return err
	}
	var projectID, provider, status, verification string
	var revision int64
	if err := tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT project_id, provider, status, verification_state, revision FROM dns_accounts WHERE id = ?`+r.resourceWriteLock()), item.DNSAccountID).
		Scan(&projectID, &provider, &status, &verification, &revision); err != nil {
		return ErrDNSBindingInvalid
	}
	if projectID != item.ProjectID {
		return ErrProjectAccessDenied
	}
	if revision != item.InitialDNSRevision {
		return ErrDNSAccountConflict
	}
	if provider != item.DNSProvider || status != domain.DNSAccountActive || verification != domain.DNSAccountVerified {
		return ErrDNSBindingInvalid
	}
	return nil
}
