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

var ErrProjectDomainBusy = errors.New("域名存在正在执行的任务，暂不能删除")

var _ ProjectDomainRepository = (*SQLProjectMemberRepository)(nil)

func (r *SQLProjectMemberRepository) CreateProjectDomain(ctx context.Context, actorID string, item domain.Domain) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	item.ResourceScope = item.ResourceScope.Normalize()
	payload, err := json.Marshal(item.SANs)
	if err != nil {
		return fmt.Errorf("编码域名 SAN 失败：%w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始域名创建事务失败：%w", err)
	}
	defer tx.Rollback()
	if err := r.authorizeProjectTransaction(ctx, tx, actorID, item.ProjectID, domain.PermissionCertificateManage); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, Rebind(r.driver, `INSERT INTO domains
		(id, name, certificate_type, sans_json, auto_renew, acme_environment, dns_provider, status, expires_at, created_at, updated_at, project_id, owner_id, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), item.ID, item.Name, item.CertificateType, string(payload), item.AutoRenew,
		item.ACMEEnvironment, item.DNSProvider, item.Status, nullableTime(item.ExpiresAt), item.CreatedAt, item.UpdatedAt,
		item.ProjectID, item.OwnerID, item.CreatedBy, item.UpdatedBy)
	if err != nil {
		return fmt.Errorf("保存域名失败：%w", err)
	}
	if err := writeDomainAudit(ctx, tx, r.driver, "domain.create", item.ID, item.ProjectID, actorID, "创建域名"); err != nil {
		return err
	}
	return commitProjectDomain(tx, "提交域名创建事务失败")
}

func (r *SQLProjectMemberRepository) UpdateProjectDomain(ctx context.Context, actorID string, item domain.Domain) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	payload, err := json.Marshal(item.SANs)
	if err != nil {
		return fmt.Errorf("编码域名 SAN 失败：%w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始域名更新事务失败：%w", err)
	}
	defer tx.Rollback()
	var stored domain.Domain
	var certificateType string
	if err := tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT id, name, certificate_type, project_id FROM domains WHERE id = ?`+r.resourceWriteLock()), item.ID).
		Scan(&stored.ID, &stored.Name, &certificateType, &stored.ProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("读取待更新域名失败：%w", err)
	}
	stored.CertificateType = domain.CertificateType(certificateType)
	if err := r.authorizeProjectTransaction(ctx, tx, actorID, stored.ProjectID, domain.PermissionCertificateManage); err != nil {
		return err
	}
	item.ProjectID = stored.ProjectID
	if err := r.validateDomainPathsInTransaction(ctx, tx, item); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, Rebind(r.driver, `UPDATE domains SET
		name = ?, certificate_type = ?, sans_json = ?, auto_renew = ?, acme_environment = ?, dns_provider = ?, updated_at = ?, updated_by = ?
		WHERE id = ? AND project_id = ?`), item.Name, item.CertificateType, string(payload), item.AutoRenew, item.ACMEEnvironment,
		item.DNSProvider, item.UpdatedAt, item.UpdatedBy, item.ID, stored.ProjectID)
	if err != nil {
		return fmt.Errorf("更新域名失败：%w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return sql.ErrNoRows
	}
	if err := writeDomainAudit(ctx, tx, r.driver, "domain.update", item.ID, stored.ProjectID, actorID, "更新域名"); err != nil {
		return err
	}
	return commitProjectDomain(tx, "提交域名更新事务失败")
}

func (r *SQLProjectMemberRepository) DeleteProjectDomain(ctx context.Context, actorID, domainID string) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始域名删除事务失败：%w", err)
	}
	defer tx.Rollback()
	var projectID string
	if err := tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT project_id FROM domains WHERE id = ?`+r.resourceWriteLock()), domainID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("读取待删除域名失败：%w", err)
	}
	if err := r.authorizeProjectTransaction(ctx, tx, actorID, projectID, domain.PermissionCertificateManage); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, Rebind(r.driver, `SELECT id FROM tasks
		WHERE domain_id = ? AND task_type IN ('certificate_issue', 'certificate_validate', 'certificate_deploy', 'certificate_import', 'certificate_restore')
		AND status IN ('queued', 'running')`+r.resourceWriteLock()), domainID)
	if err != nil {
		return fmt.Errorf("检查域名任务失败：%w", err)
	}
	busy := rows.Next()
	rows.Close()
	if busy {
		return ErrProjectDomainBusy
	}
	if _, err := tx.ExecContext(ctx, Rebind(r.driver, `DELETE FROM domain_servers WHERE domain_id = ?`), domainID); err != nil {
		return fmt.Errorf("删除域名服务器关联失败：%w", err)
	}
	result, err := tx.ExecContext(ctx, Rebind(r.driver, `DELETE FROM domains WHERE id = ? AND project_id = ?`), domainID, projectID)
	if err != nil {
		return fmt.Errorf("删除域名失败：%w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return sql.ErrNoRows
	}
	if err := writeDomainAudit(ctx, tx, r.driver, "domain.delete", domainID, projectID, actorID, "删除域名"); err != nil {
		return err
	}
	return commitProjectDomain(tx, "提交域名删除事务失败")
}

func (r *SQLProjectMemberRepository) validateDomainPathsInTransaction(ctx context.Context, tx *sql.Tx, item domain.Domain) error {
	rows, err := tx.QueryContext(ctx, Rebind(r.driver, serverSelectQualified+` INNER JOIN domain_servers ds ON ds.server_id = servers.id WHERE ds.domain_id = ? ORDER BY servers.id`+r.resourceWriteLock()), item.ID)
	if err != nil {
		return fmt.Errorf("查询域名服务器路径失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		server, err := scanServer(rows)
		if err != nil {
			return err
		}
		if server.ProjectID != item.ProjectID {
			return ErrProjectAccessDenied
		}
		if err := validateServerDomainPaths(ctx, tx, r.driver, server, &item); err != nil {
			return err
		}
	}
	return rows.Err()
}

func writeDomainAudit(ctx context.Context, tx *sql.Tx, driver, action, domainID, projectID, actorID, message string) error {
	result, err := tx.ExecContext(ctx, Rebind(driver, `INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		VALUES (?, 'domain', ?, 'success', ?, ?, 'project', ?, '', ?)`), action, domainID, message, time.Now().UTC(), projectID, actorID)
	if err != nil {
		return fmt.Errorf("写入域名操作审计失败：%w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errors.New("域名操作审计未写入，业务变更已回滚")
	}
	return nil
}

func commitProjectDomain(tx *sql.Tx, message string) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s：%w", message, err)
	}
	return nil
}
