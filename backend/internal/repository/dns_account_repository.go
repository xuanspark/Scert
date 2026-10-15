package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

var ErrDNSAccountConflict = errors.New("DNS 账户已变更，请刷新后重试")

var ErrLegacyDNSAccountConflict = errors.New("旧系统 DNS 账户标识已被其他账户占用")

// DNSAccountRepository 的公开读取不返回凭据引用；秘密读取只能使用独立的使用权限入口。
// 删除及运行租约将在域名绑定接入后提供，当前不提供无引用保护的删除方法。
type DNSAccountRepository interface {
	CreateDNSAccount(context.Context, string, domain.DNSAccount) (domain.DNSAccount, error)
	ListVisibleDNSAccounts(context.Context, string) ([]domain.DNSAccount, error)
	GetVisibleDNSAccount(context.Context, string, string) (domain.DNSAccount, error)
	GetDNSAccountForUse(context.Context, string, string, string) (domain.DNSAccount, error)
	GetDNSAccountForVerification(context.Context, string, string) (domain.DNSAccount, error)
	UpdateDNSAccount(context.Context, string, string, int64, string, string) (domain.DNSAccount, error)
	RenameDNSAccount(context.Context, string, string, int64, string) (domain.DNSAccount, error)
	SetDNSAccountEnabled(context.Context, string, string, int64, bool) (domain.DNSAccount, error)
	RotateDNSAccountCredential(context.Context, string, string, int64, string) (domain.DNSAccount, error)
	RecordDNSAccountVerification(context.Context, string, string, int64, bool) (domain.DNSAccount, error)
}

// DNSLegacyAccountImporter 只负责把旧配置登记为可追踪的账户元数据。
// 它不读取或复制秘密，也不把旧验证状态冒充为新账户已在线验证。
type DNSLegacyAccountImporter interface {
	ImportLegacyDNSAccount(context.Context, domain.DNSAccount) error
}

var _ DNSAccountRepository = (*SQLProjectMemberRepository)(nil)
var _ DNSLegacyAccountImporter = (*SQLProjectMemberRepository)(nil)

const dnsAccountColumns = `dns_accounts.id, dns_accounts.name, dns_accounts.project_id,
	dns_accounts.owner_user_id, dns_accounts.provider, dns_accounts.credential_ref,
	dns_accounts.status, dns_accounts.verification_state, dns_accounts.revision,
	dns_accounts.created_at, dns_accounts.updated_at`

// ImportLegacyDNSAccount 将旧的单账户配置幂等登记到独立账户表。
// 已存在同凭据引用时保持现有记录不变；同一固定 ID 被其他账户占用时拒绝启动迁移，避免覆盖用户修改。
func (r *SQLProjectMemberRepository) ImportLegacyDNSAccount(ctx context.Context, item domain.DNSAccount) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	item.Name = strings.TrimSpace(item.Name)
	item.Status = domain.DNSAccountActive
	item.VerificationState = domain.DNSAccountUnverified
	item.Revision = 1
	if err := item.Validate(); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existingID string
	err = tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT id FROM dns_accounts WHERE credential_ref = ?`+r.resourceWriteLock()), item.CredentialRef).Scan(&existingID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var existingRef string
	err = tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT credential_ref FROM dns_accounts WHERE id = ?`+r.resourceWriteLock()), item.ID).Scan(&existingRef)
	if err == nil {
		if existingRef == item.CredentialRef {
			return nil
		}
		return ErrLegacyDNSAccountConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var projectID string
	if err := tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT id FROM projects WHERE id = ?`), item.ProjectID).Scan(&projectID); err != nil {
		return fmt.Errorf("旧 DNS 账户所属项目不存在：%w", err)
	}
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	if _, err := tx.ExecContext(ctx, Rebind(r.driver, `INSERT INTO dns_accounts
		(id, name, project_id, owner_user_id, provider, credential_ref, status, verification_state, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), item.ID, item.Name, item.ProjectID, item.OwnerUserID,
		item.Provider, item.CredentialRef, item.Status, item.VerificationState, item.Revision, item.CreatedAt, item.UpdatedAt); err != nil {
		return errors.New("登记旧 DNS 账户失败")
	}
	if err := writeDNSAccountAudit(ctx, tx, r.driver, item.OwnerUserID, item, "dns_account.import_legacy"); err != nil {
		return err
	}
	return tx.Commit()
}

func scanDNSAccount(row scanner) (domain.DNSAccount, error) {
	var item domain.DNSAccount
	err := row.Scan(&item.ID, &item.Name, &item.ProjectID, &item.OwnerUserID, &item.Provider,
		&item.CredentialRef, &item.Status, &item.VerificationState, &item.Revision, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func publicDNSAccount(item domain.DNSAccount) domain.DNSAccount {
	item.CredentialRef = ""
	return item
}

func (r *SQLProjectMemberRepository) CreateDNSAccount(ctx context.Context, actorID string, item domain.DNSAccount) (domain.DNSAccount, error) {
	if err := r.ensureDB(); err != nil {
		return domain.DNSAccount{}, err
	}
	// 用户归属、初始验证状态及版本只由服务端生成，不能由请求冒领或伪造验证成功。
	item.OwnerUserID, item.Revision = actorID, 1
	item.Status, item.VerificationState = domain.DNSAccountActive, domain.DNSAccountUnverified
	item.CreatedAt = time.Now().UTC()
	item.UpdatedAt = item.CreatedAt
	if err := item.Validate(); err != nil {
		return domain.DNSAccount{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.DNSAccount{}, err
	}
	defer tx.Rollback()
	if err := r.authorizeProjectTransaction(ctx, tx, actorID, item.ProjectID, domain.PermissionDNSManage); err != nil {
		return domain.DNSAccount{}, err
	}
	_, err = tx.ExecContext(ctx, Rebind(r.driver, `INSERT INTO dns_accounts
		(id, name, project_id, owner_user_id, provider, credential_ref, status, verification_state, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), item.ID, item.Name, item.ProjectID, item.OwnerUserID,
		item.Provider, item.CredentialRef, item.Status, item.VerificationState, item.Revision, item.CreatedAt, item.UpdatedAt)
	if err != nil {
		// 驱动错误可能回显唯一键中的凭据引用，不把原始错误传向外部接口。
		return domain.DNSAccount{}, errors.New("保存 DNS 账户失败，请检查账户或凭据引用是否重复")
	}
	if err := writeDNSAccountAudit(ctx, tx, r.driver, actorID, item, "dns_account.create"); err != nil {
		return domain.DNSAccount{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.DNSAccount{}, err
	}
	return publicDNSAccount(item), nil
}

func (r *SQLProjectMemberRepository) ListVisibleDNSAccounts(ctx context.Context, actorID string) ([]domain.DNSAccount, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return nil, err
	}
	predicate, args := resourceReadPredicate("dns_accounts.project_id", actorID, domain.PermissionDNSRead)
	rows, err := r.db.QueryContext(ctx, Rebind(r.driver, `SELECT `+dnsAccountColumns+` FROM dns_accounts WHERE `+predicate+` ORDER BY dns_accounts.name, dns_accounts.id`), args...)
	if err != nil {
		return nil, fmt.Errorf("读取 DNS 账户列表失败：%w", err)
	}
	defer rows.Close()
	items := make([]domain.DNSAccount, 0)
	for rows.Next() {
		item, err := scanDNSAccount(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, publicDNSAccount(item))
	}
	return items, rows.Err()
}

func (r *SQLProjectMemberRepository) GetVisibleDNSAccount(ctx context.Context, actorID, id string) (domain.DNSAccount, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return domain.DNSAccount{}, err
	}
	predicate, args := resourceReadPredicate("dns_accounts.project_id", actorID, domain.PermissionDNSRead)
	args = append(args, id)
	item, err := scanDNSAccount(r.db.QueryRowContext(ctx, Rebind(r.driver, `SELECT `+dnsAccountColumns+` FROM dns_accounts WHERE `+predicate+` AND dns_accounts.id = ?`), args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DNSAccount{}, ErrProjectAccessDenied
	}
	return publicDNSAccount(item), err
}

// GetDNSAccountForUse 要求调用方传真实域名项目；禁用、未验证或跨项目账户不返回秘密引用。
// 返回的是检查时快照，签发执行仍须通过后续租约机制持有版本，不能把此读取当作执行锁。
func (r *SQLProjectMemberRepository) GetDNSAccountForUse(ctx context.Context, actorID, id, projectID string) (domain.DNSAccount, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return domain.DNSAccount{}, err
	}
	predicate, args := resourceReadPredicate("dns_accounts.project_id", actorID, domain.PermissionDNSCredentialUse)
	args = append(args, id, projectID, domain.DNSAccountActive, domain.DNSAccountVerified)
	item, err := scanDNSAccount(r.db.QueryRowContext(ctx, Rebind(r.driver, `SELECT `+dnsAccountColumns+` FROM dns_accounts WHERE `+predicate+`
		AND dns_accounts.id = ? AND dns_accounts.project_id = ? AND dns_accounts.status = ? AND dns_accounts.verification_state = ?`), args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DNSAccount{}, ErrProjectAccessDenied
	}
	return item, err
}

func (r *SQLProjectMemberRepository) RenameDNSAccount(ctx context.Context, actorID, id string, revision int64, name string) (domain.DNSAccount, error) {
	return r.changeDNSAccount(ctx, actorID, id, revision, "dns_account.rename", func(item *domain.DNSAccount) error {
		item.Name = name
		return nil
	})
}

func (r *SQLProjectMemberRepository) UpdateDNSAccount(ctx context.Context, actorID, id string, revision int64, name, reference string) (domain.DNSAccount, error) {
	return r.changeDNSAccount(ctx, actorID, id, revision, "dns_account.update", func(item *domain.DNSAccount) error {
		item.Name = name
		if reference != "" {
			item.CredentialRef = reference
			item.VerificationState = domain.DNSAccountUnverified
		}
		return nil
	})
}

// 首次验证允许读取尚未验证的账户，但必须实时拥有管理权限，且账户未被禁用。
// 结果落库仍须核对读取时版本，不能将此快照作为长时间授权或执行租约。
func (r *SQLProjectMemberRepository) GetDNSAccountForVerification(ctx context.Context, actorID, id string) (domain.DNSAccount, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return domain.DNSAccount{}, err
	}
	predicate, args := resourceReadPredicate("dns_accounts.project_id", actorID, domain.PermissionDNSManage)
	args = append(args, id, domain.DNSAccountActive)
	item, err := scanDNSAccount(r.db.QueryRowContext(ctx, Rebind(r.driver, `SELECT `+dnsAccountColumns+` FROM dns_accounts WHERE `+predicate+`
		AND dns_accounts.id = ? AND dns_accounts.status = ?`), args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DNSAccount{}, ErrProjectAccessDenied
	}
	return item, err
}

func (r *SQLProjectMemberRepository) SetDNSAccountEnabled(ctx context.Context, actorID, id string, revision int64, enabled bool) (domain.DNSAccount, error) {
	return r.changeDNSAccount(ctx, actorID, id, revision, "dns_account.status", func(item *domain.DNSAccount) error {
		item.Status = domain.DNSAccountDisabled
		if enabled {
			item.Status = domain.DNSAccountActive
		}
		return nil
	})
}

func (r *SQLProjectMemberRepository) RotateDNSAccountCredential(ctx context.Context, actorID, id string, revision int64, reference string) (domain.DNSAccount, error) {
	return r.changeDNSAccount(ctx, actorID, id, revision, "dns_account.rotate", func(item *domain.DNSAccount) error {
		if reference == item.CredentialRef {
			return errors.New("DNS 凭据轮换必须使用新的秘密引用")
		}
		item.CredentialRef, item.VerificationState = reference, domain.DNSAccountUnverified
		return nil
	})
}

// 验证调用者必须先完成真实 DNS 验证；此入口只接受开始验证时的版本，不接受客户端自报结果。
func (r *SQLProjectMemberRepository) RecordDNSAccountVerification(ctx context.Context, actorID, id string, revision int64, success bool) (domain.DNSAccount, error) {
	return r.changeDNSAccount(ctx, actorID, id, revision, "dns_account.verify", func(item *domain.DNSAccount) error {
		if item.Status != domain.DNSAccountActive {
			return errors.New("已禁用的 DNS 账户不能完成验证")
		}
		item.VerificationState = domain.DNSAccountVerificationFailed
		if success {
			item.VerificationState = domain.DNSAccountVerified
		}
		return nil
	})
}

func (r *SQLProjectMemberRepository) changeDNSAccount(ctx context.Context, actorID, id string, revision int64, action string, change func(*domain.DNSAccount) error) (domain.DNSAccount, error) {
	if err := r.ensureDB(); err != nil {
		return domain.DNSAccount{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.DNSAccount{}, err
	}
	defer tx.Rollback()
	item, err := scanDNSAccount(tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT `+dnsAccountColumns+` FROM dns_accounts WHERE id = ?`+r.resourceWriteLock()), id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DNSAccount{}, ErrProjectAccessDenied
	}
	if err != nil {
		return domain.DNSAccount{}, err
	}
	if err := r.authorizeProjectTransaction(ctx, tx, actorID, item.ProjectID, domain.PermissionDNSManage); err != nil {
		return domain.DNSAccount{}, err
	}
	if item.Revision != revision {
		return domain.DNSAccount{}, ErrDNSAccountConflict
	}
	if err := r.rejectActiveDNSLease(ctx, tx, "account_id", id); err != nil {
		return domain.DNSAccount{}, err
	}
	if err := change(&item); err != nil {
		return domain.DNSAccount{}, err
	}
	item.Revision++
	item.UpdatedAt = time.Now().UTC()
	if err := item.Validate(); err != nil {
		return domain.DNSAccount{}, err
	}
	result, err := tx.ExecContext(ctx, Rebind(r.driver, `UPDATE dns_accounts SET name = ?, credential_ref = ?, status = ?, verification_state = ?, revision = ?, updated_at = ? WHERE id = ? AND revision = ?`),
		item.Name, item.CredentialRef, item.Status, item.VerificationState, item.Revision, item.UpdatedAt, item.ID, revision)
	if err != nil {
		if r.driver == "sqlite" && isSQLiteWriteConflict(err) {
			return domain.DNSAccount{}, ErrDNSAccountConflict
		}
		return domain.DNSAccount{}, errors.New("更新 DNS 账户失败，请检查凭据引用是否重复")
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return domain.DNSAccount{}, ErrDNSAccountConflict
	}
	if err := writeDNSAccountAudit(ctx, tx, r.driver, actorID, item, action); err != nil {
		return domain.DNSAccount{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.DNSAccount{}, err
	}
	return publicDNSAccount(item), nil
}

// 使用驱动错误码识别忙与锁冲突，不依赖可能包含秘密或本地化文本的错误消息。
func isSQLiteWriteConflict(err error) bool {
	var coded interface{ Code() int }
	if !errors.As(err, &coded) {
		return false
	}
	code := coded.Code() & 0xff
	return code == 5 || code == 6
}

func writeDNSAccountAudit(ctx context.Context, tx *sql.Tx, driver, actorID string, item domain.DNSAccount, action string) error {
	result, err := tx.ExecContext(ctx, Rebind(driver, `INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		VALUES (?, 'dns_account', ?, 'success', 'DNS 账户资料已更新', ?, 'project', ?, '', ?)`), action, item.ID, time.Now().UTC(), item.ProjectID, actorID)
	if err != nil {
		return errors.New("写入 DNS 账户审计失败，变更已回滚")
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errors.New("DNS 账户审计未保存，变更已回滚")
	}
	return nil
}
