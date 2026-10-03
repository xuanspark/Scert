package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

var (
	ErrDNSExecutionInvalid = errors.New("DNS 执行租约或任务授权已失效")
	ErrDNSAccountBusy      = errors.New("DNS 账户仍有执行租约，需等待任务及挑战清理完成")
)

// 租约不设置自动到期：外部调用或清理结果不确定时必须保留秘密引用。
func (r *SQLProjectMemberRepository) AcquireDNSExecutionLease(ctx context.Context, item domain.Domain, identity domain.DNSExecutionIdentity) (domain.DNSExecutionLease, error) {
	if err := r.ensureDB(); err != nil {
		return domain.DNSExecutionLease{}, err
	}
	if item.DNSAccountID == "" || item.DNSBindingID == "" || !domain.ValidProjectID(identity.TaskID) || !domain.ValidProjectID(identity.AttemptID) || identity.AttemptNumber < 1 {
		return domain.DNSExecutionLease{}, ErrDNSExecutionInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.DNSExecutionLease{}, err
	}
	defer tx.Rollback()
	lease := domain.DNSExecutionLease{ID: domain.NewID(), DomainID: item.ID, ProjectID: item.ResourceScope.Normalize().ProjectID,
		BindingID: item.DNSBindingID, AccountID: item.DNSAccountID, Identity: identity}
	account, err := r.authorizeDNSExecution(ctx, tx, lease)
	if err != nil {
		return domain.DNSExecutionLease{}, err
	}
	if err := r.rejectActiveDNSLease(ctx, tx, "domain_id", item.ID); err != nil {
		return domain.DNSExecutionLease{}, err
	}
	// 域名行锁与资料更新共用，拒绝授权前后被换掉的签发快照。
	var updated time.Time
	if err := tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT updated_at FROM domains WHERE id = ?`), item.ID).Scan(&updated); err != nil {
		return domain.DNSExecutionLease{}, err
	}
	if !updated.Equal(item.UpdatedAt) {
		return domain.DNSExecutionLease{}, ErrDNSExecutionInvalid
	}
	lease.Revision, lease.Provider, lease.CredentialRef = account.Revision, account.Provider, account.CredentialRef
	now := time.Now().UTC()
	scheduled := 0
	if identity.Scheduled {
		scheduled = 1
	}
	_, err = tx.ExecContext(ctx, Rebind(r.driver, `INSERT INTO dns_execution_leases
		(id, domain_id, binding_id, project_id, account_id, account_revision, provider, credential_ref,
		task_id, attempt_id, attempt_number, scheduled, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?)`), lease.ID, lease.DomainID, lease.BindingID,
		lease.ProjectID, lease.AccountID, lease.Revision, lease.Provider, lease.CredentialRef,
		identity.TaskID, identity.AttemptID, identity.AttemptNumber, scheduled, now, now)
	if err != nil {
		return domain.DNSExecutionLease{}, ErrDNSExecutionInvalid
	}
	if err := tx.Commit(); err != nil {
		return domain.DNSExecutionLease{}, ErrDNSExecutionInvalid
	}
	return lease, nil
}

// 所有校验在短事务内完成；网络调用期间不占用数据库事务。
// 加锁顺序为域名、任务、尝试、账户，账户变更在同一账户行上串行化。
func (r *SQLProjectMemberRepository) authorizeDNSExecution(ctx context.Context, tx *sql.Tx, lease domain.DNSExecutionLease) (domain.DNSAccount, error) {
	var projectID, challengeType string
	var autoRenew bool
	err := tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT project_id, challenge_type, auto_renew FROM domains WHERE id = ?`+r.resourceWriteLock()), lease.DomainID).Scan(&projectID, &challengeType, &autoRenew)
	if err != nil || projectID != lease.ProjectID || challengeType != domain.ChallengeDNS01 {
		return domain.DNSAccount{}, ErrDNSExecutionInvalid
	}
	var bindingID, accountID string
	err = tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT id, dns_account_id FROM domain_dns_accounts WHERE domain_id = ? AND project_id = ?`+r.resourceWriteLock()), lease.DomainID, projectID).Scan(&bindingID, &accountID)
	if err != nil || bindingID != lease.BindingID || accountID != lease.AccountID {
		return domain.DNSAccount{}, ErrDNSExecutionInvalid
	}
	var taskType, actorID, taskDomain, taskProject, taskStatus, taskScope string
	err = tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT task_type, operator_id, domain_id, project_id, status, scope_type FROM tasks WHERE id = ?`+r.resourceWriteLock()), lease.Identity.TaskID).
		Scan(&taskType, &actorID, &taskDomain, &taskProject, &taskStatus, &taskScope)
	if err != nil || taskDomain != lease.DomainID || taskProject != projectID || taskStatus != "running" || taskScope != ScopeProject {
		return domain.DNSAccount{}, ErrDNSExecutionInvalid
	}
	var number int
	var attemptTask, attemptStatus string
	err = tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT task_id, attempt_number, status FROM task_attempts WHERE id = ?`+r.resourceWriteLock()), lease.Identity.AttemptID).
		Scan(&attemptTask, &number, &attemptStatus)
	if err != nil || attemptTask != lease.Identity.TaskID || number != lease.Identity.AttemptNumber || attemptStatus != "running" {
		return domain.DNSAccount{}, ErrDNSExecutionInvalid
	}
	if lease.Identity.Scheduled {
		if taskType != "renewal_run" || actorID != "" || !autoRenew {
			return domain.DNSAccount{}, ErrDNSExecutionInvalid
		}
	} else {
		if actorID == "" || (taskType != "certificate_issue" && taskType != "renewal_run") {
			return domain.DNSAccount{}, ErrDNSExecutionInvalid
		}
		permissions := []domain.ProjectPermission{domain.PermissionCertificateManage, domain.PermissionDeploymentExecute, domain.PermissionDNSCredentialUse}
		if number > 1 {
			permissions = append(permissions, domain.PermissionTaskRetry)
		}
		if err := r.authorizeProjectTransaction(ctx, tx, actorID, projectID, permissions...); err != nil {
			return domain.DNSAccount{}, err
		}
	}
	account, err := scanDNSAccount(tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT `+dnsAccountColumns+` FROM dns_accounts WHERE id = ?`+r.resourceWriteLock()), accountID))
	if err != nil || account.ProjectID != projectID || account.Status != domain.DNSAccountActive || account.VerificationState != domain.DNSAccountVerified {
		return domain.DNSAccount{}, ErrDNSExecutionInvalid
	}
	if err := account.Validate(); err != nil {
		return domain.DNSAccount{}, ErrDNSExecutionInvalid
	}
	return account, nil
}

func (r *SQLProjectMemberRepository) ValidateDNSExecutionLease(ctx context.Context, lease domain.DNSExecutionLease) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	account, err := r.authorizeDNSExecution(ctx, tx, lease)
	if err != nil {
		return err
	}
	if account.Revision != lease.Revision || account.Provider != lease.Provider || account.CredentialRef != lease.CredentialRef {
		return ErrDNSExecutionInvalid
	}
	var id string
	scheduled := 0
	if lease.Identity.Scheduled {
		scheduled = 1
	}
	err = tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT id FROM dns_execution_leases WHERE id = ? AND domain_id = ?
		AND binding_id = ? AND project_id = ? AND account_id = ? AND account_revision = ? AND provider = ? AND credential_ref = ?
		AND task_id = ? AND attempt_id = ? AND attempt_number = ? AND scheduled = ? AND status = 'active'`+r.resourceWriteLock()),
		lease.ID, lease.DomainID, lease.BindingID, lease.ProjectID, lease.AccountID, lease.Revision, lease.Provider, lease.CredentialRef,
		lease.Identity.TaskID, lease.Identity.AttemptID, lease.Identity.AttemptNumber, scheduled).Scan(&id)
	if err != nil {
		return ErrDNSExecutionInvalid
	}
	return tx.Commit()
}

// 释放只接受持有方随机能力标识，取消和撤权不能妨碍原凭据的清理收尾。
func (r *SQLProjectMemberRepository) ReleaseDNSExecutionLease(ctx context.Context, id string) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	if !domain.ValidProjectID(id) {
		return ErrDNSExecutionInvalid
	}
	_, err := r.db.ExecContext(ctx, Rebind(r.driver, `UPDATE dns_execution_leases SET status = 'released', updated_at = ? WHERE id = ? AND status = 'active'`), time.Now().UTC(), id)
	if err != nil {
		return ErrDNSExecutionInvalid
	}
	var status string
	if err := r.db.QueryRowContext(ctx, Rebind(r.driver, `SELECT status FROM dns_execution_leases WHERE id = ?`), id).Scan(&status); err != nil || status != "released" {
		return ErrDNSExecutionInvalid
	}
	return nil
}

func (r *SQLProjectMemberRepository) rejectActiveDNSLease(ctx context.Context, tx *sql.Tx, column, id string) error {
	if column != "account_id" && column != "domain_id" {
		return ErrDNSExecutionInvalid
	}
	var active string
	err := tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT id FROM dns_execution_leases WHERE `+column+` = ? AND status = 'active' LIMIT 1`+r.resourceWriteLock()), id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrDNSAccountBusy
}
