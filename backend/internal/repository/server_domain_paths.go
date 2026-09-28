package repository

import (
	"context"
	"database/sql"
	"fmt"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func serverResourceLock(driver string) string {
	if driver == "mysql" || driver == "postgres" {
		return " FOR UPDATE"
	}
	return ""
}

// 调用者须在同一事务先锁定服务器行；候选域名用于校验尚未落库的新关联。
func validateServerDomainPaths(ctx context.Context, tx *sql.Tx, driver string, server deployment.Server, candidate *domain.Domain) error {
	excludeID := ""
	items := make([]domain.Domain, 0)
	if candidate != nil {
		excludeID = candidate.ID
		items = append(items, *candidate)
	}
	rows, err := tx.QueryContext(ctx, Rebind(driver, `SELECT d.id, d.name, d.certificate_type
		FROM domains d INNER JOIN domain_servers ds ON ds.domain_id = d.id
		WHERE ds.server_id = ? AND d.id <> ? ORDER BY d.id`+serverResourceLock(driver)), server.ID, excludeID)
	if err != nil {
		return fmt.Errorf("查询共享服务器的域名路径失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.Domain
		if err := rows.Scan(&item.ID, &item.Name, &item.CertificateType); err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return deployment.ValidateServerDomainPaths(server, items)
}

// 部署入口重新读取关联及路径；不能把保存关联时的校验当成永久有效。
// 返回的服务器配置与路径检查使用同一事务快照，事务完成后才允许进行远端操作。
func (r *SQLServerRepository) ListDeploymentServersByDomainID(ctx context.Context, domainID string) ([]deployment.Server, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var candidate domain.Domain
	if err := tx.QueryRowContext(ctx, r.query(`SELECT id, project_id, name, certificate_type FROM domains WHERE id = ?`+serverResourceLock(r.driver)), domainID).Scan(&candidate.ID, &candidate.ProjectID, &candidate.Name, &candidate.CertificateType); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, r.query(serverSelectQualified+` INNER JOIN domain_servers ds
		ON ds.server_id = servers.id WHERE ds.domain_id = ? ORDER BY servers.id`+serverResourceLock(r.driver)), domainID)
	if err != nil {
		return nil, err
	}
	items := make([]deployment.Server, 0)
	for rows.Next() {
		item, err := scanServer(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for index, item := range items {
		if item.ProjectID != candidate.ProjectID {
			return nil, ErrProjectAccessDenied
		}
		if err := validateServerDomainPaths(ctx, tx, r.driver, item, nil); err != nil {
			return nil, err
		}
		resolved, err := deployment.ResolveServerPaths(item, candidate.Name, string(candidate.CertificateType))
		if err != nil {
			return nil, err
		}
		items[index] = resolved
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}
