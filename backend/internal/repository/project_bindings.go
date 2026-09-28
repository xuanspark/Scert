package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

const MaxProjectBindingServers = 100

var ErrProjectBindingInput = errors.New("服务器列表必须明确提供，最多包含 100 个有效服务器标识")

type ProjectBindingFailure struct {
	ActorID    string
	DomainID   string
	StatusCode int
	RequestID  string
}

type ProjectBindingRepository interface {
	ReplaceProjectDomainServers(context.Context, string, string, []string) ([]deployment.Server, error)
	RecordProjectBindingFailure(context.Context, ProjectBindingFailure) error
}

var _ ProjectBindingRepository = (*SQLProjectMemberRepository)(nil)

// 绑定变更只负责保存关联，不部署证书、不连接 SSH、不接收客户端的项目归属。
func (r *SQLProjectMemberRepository) ReplaceProjectDomainServers(ctx context.Context, actorID, domainID string, serverIDs []string) ([]deployment.Server, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return nil, err
	}
	if domainID == "" || len(domainID) > 128 {
		return nil, ErrProjectAccessDenied
	}
	if serverIDs == nil || len(serverIDs) > MaxProjectBindingServers {
		return nil, ErrProjectBindingInput
	}
	ids := make([]string, 0, len(serverIDs))
	seen := make(map[string]bool, len(serverIDs))
	for _, id := range serverIDs {
		if id == "" || len(id) > 128 || strings.TrimSpace(id) != id {
			return nil, ErrProjectBindingInput
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	// 所有请求按相同次序锁定服务器，降低反向选择时的死锁概率。
	sort.Strings(ids)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始项目绑定事务失败：%w", err)
	}
	defer tx.Rollback()
	if r.driver == "mysql" {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema=DATABASE() AND table_name IN
			('users', 'projects', 'project_members', 'domains', 'servers', 'domain_servers', 'audit_logs')
			AND engine='InnoDB'`).Scan(&count); err != nil {
			return nil, fmt.Errorf("检查项目绑定存储失败：%w", err)
		}
		if count != 7 {
			return nil, errors.New("项目绑定及授权审计相关表必须使用 InnoDB")
		}
	}
	var candidate domain.Domain
	if err := tx.QueryRowContext(ctx, Rebind(r.driver,
		`SELECT id, project_id, name, certificate_type FROM domains WHERE id = ?`+r.resourceWriteLock()), domainID).Scan(&candidate.ID, &candidate.ProjectID, &candidate.Name, &candidate.CertificateType); err != nil {
		return nil, projectBindingReadError(err)
	}
	projectID := candidate.ProjectID
	if err := r.authorizeBindingTransaction(ctx, tx, actorID, projectID); err != nil {
		return nil, err
	}
	items := make([]deployment.Server, 0, len(ids))
	for _, id := range ids {
		item, err := scanServer(tx.QueryRowContext(ctx, Rebind(r.driver,
			serverSelectQualified+` WHERE servers.id = ?`+r.resourceWriteLock()), id))
		if err != nil {
			return nil, projectBindingReadError(err)
		}
		if item.ProjectID != projectID {
			return nil, ErrProjectAccessDenied
		}
		items = append(items, item)
	}
	for _, item := range items {
		if err := validateServerDomainPaths(ctx, tx, r.driver, item, &candidate); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, Rebind(r.driver, `DELETE FROM domain_servers WHERE domain_id = ?`), domainID); err != nil {
		return nil, fmt.Errorf("清理项目绑定失败：%w", err)
	}
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, Rebind(r.driver,
			`INSERT INTO domain_servers (domain_id, server_id, project_id) VALUES (?, ?, ?)`), domainID, id, projectID)
		if err != nil {
			return nil, fmt.Errorf("保存项目绑定失败：%w", err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return nil, errors.New("项目绑定未完整写入，已回滚本次变更")
		}
	}
	result, err := tx.ExecContext(ctx, Rebind(r.driver, `INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		VALUES ('domain.servers.replace', 'domain', ?, 'success', ?, ?, 'project', ?, '', ?)`),
		domainID, fmt.Sprintf("已更新部署服务器绑定，共 %d 台", len(ids)), time.Now().UTC(), projectID, actorID)
	if err != nil {
		return nil, fmt.Errorf("保存项目绑定审计失败：%w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return nil, errors.New("项目绑定审计未写入，已回滚本次变更")
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交项目绑定失败：%w", err)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].ID < items[j].ID
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func (r *SQLProjectMemberRepository) resourceWriteLock() string {
	if r.driver == "mysql" || r.driver == "postgres" {
		return " FOR UPDATE"
	}
	return ""
}

func projectBindingReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrProjectAccessDenied
	}
	return fmt.Errorf("读取项目绑定资源失败：%w", err)
}

// 使用事务内的当前读取并持有账户、项目和现有成员行锁，避免撤权或停用后继续写入。
// SQLite 使用自身事务串行写入保证；遇到并发升级失败时回滚，不绕过授权重试。
func (r *SQLProjectMemberRepository) authorizeBindingTransaction(ctx context.Context, tx *sql.Tx, actorID, projectID string) error {
	var status, userRole, existingProject string
	if err := tx.QueryRowContext(ctx, Rebind(r.driver,
		`SELECT status, role FROM users WHERE id = ?`+r.resourceWriteLock()), actorID).Scan(&status, &userRole); err != nil {
		return projectBindingReadError(err)
	}
	if status != UserStatusActive || (userRole != UserRoleSuperAdmin && userRole != UserRoleAdmin && userRole != UserRoleUser) {
		return ErrProjectAccessDenied
	}
	if err := tx.QueryRowContext(ctx, Rebind(r.driver,
		`SELECT id FROM projects WHERE id = ?`+r.resourceWriteLock()), projectID).Scan(&existingProject); err != nil {
		return projectBindingReadError(err)
	}
	if userRole == UserRoleSuperAdmin {
		return nil
	}
	rows, err := tx.QueryContext(ctx, Rebind(r.driver,
		`SELECT role FROM project_members WHERE project_id = ? AND user_id = ? ORDER BY role`+r.resourceWriteLock()), projectID, actorID)
	if err != nil {
		return fmt.Errorf("读取绑定操作权限失败：%w", err)
	}
	defer rows.Close()
	permissions := map[domain.ProjectPermission]bool{
		domain.PermissionCertificateRead:   false,
		domain.PermissionServerRead:        false,
		domain.PermissionDeploymentExecute: false,
	}
	for rows.Next() {
		var role domain.ProjectRole
		if err := rows.Scan(&role); err != nil {
			return err
		}
		for permission := range permissions {
			permissions[permission] = permissions[permission] || role.Allows(permission)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, allowed := range permissions {
		if !allowed {
			return ErrProjectAccessDenied
		}
	}
	return nil
}
