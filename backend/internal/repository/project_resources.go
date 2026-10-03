package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// ProjectResourceReader 将实时权限和资源读取合并，不向外部入口暴露全量基础仓储。
type ProjectResourceReader interface {
	ListVisibleDomains(context.Context, string) ([]domain.Domain, error)
	GetVisibleDomain(context.Context, string, string) (domain.Domain, error)
	ListVisibleServers(context.Context, string) ([]deployment.Server, error)
	GetVisibleServer(context.Context, string, string) (deployment.Server, error)
	ListVisibleDomainServers(context.Context, string, string) ([]deployment.Server, error)
	ListVisibleServerDomains(context.Context, string, string) ([]domain.Domain, error)
}

var _ ProjectResourceReader = (*SQLProjectMemberRepository)(nil)

const visibleDomainSelect = `SELECT domains.id, domains.name, domains.certificate_type,
	domains.sans_json, domains.auto_renew, domains.acme_environment, domains.dns_provider,
	domains.status, domains.expires_at, domains.created_at, domains.updated_at,
	domains.project_id, domains.owner_id, domains.created_by, domains.updated_by, domains.challenge_type FROM domains`

// 列名只接受内部固定值，角色能力复用领域模型；所有身份和角色值均使用绑定参数。
// EXISTS 避免多角色账户重复返回资源，不按用户分组或全局普通管理员身份放行。
func resourceReadPredicate(projectColumn, actorID string, permission domain.ProjectPermission) (string, []any) {
	valid := projectColumn == "domains.project_id" && permission == domain.PermissionCertificateRead ||
		projectColumn == "servers.project_id" && permission == domain.PermissionServerRead ||
		projectColumn == "dns_accounts.project_id" && (permission == domain.PermissionDNSRead || permission == domain.PermissionDNSCredentialUse)
	if !valid {
		return "1 = 0", nil
	}
	roles := []domain.ProjectRole{
		domain.ProjectRoleAdmin, domain.ProjectRoleCertificateAdmin, domain.ProjectRoleDeploymentAdmin,
		domain.ProjectRoleDNSAdmin, domain.ProjectRoleAuditViewer, domain.ProjectRoleReadOnly,
	}
	args := []any{actorID}
	placeholders := make([]string, 0, len(roles))
	for _, role := range roles {
		if role.Allows(permission) {
			placeholders = append(placeholders, "?")
			args = append(args, string(role))
		}
	}
	return `EXISTS (SELECT 1 FROM projects resource_project
		JOIN users resource_actor ON resource_actor.id = ?
		WHERE resource_project.id = ` + projectColumn + ` AND resource_actor.status = 'active'
		AND (resource_actor.role = 'super_admin' OR (resource_actor.role IN ('admin', 'user')
		AND EXISTS (SELECT 1 FROM project_members resource_member
			WHERE resource_member.project_id = resource_project.id AND resource_member.user_id = resource_actor.id
			AND resource_member.role IN (` + strings.Join(placeholders, ", ") + `)))))`, args
}

func (r *SQLProjectMemberRepository) resourceReaderReady(actorID string) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	if strings.TrimSpace(actorID) == "" || len(actorID) > 128 {
		return ErrProjectAccessDenied
	}
	return nil
}

func (r *SQLProjectMemberRepository) ListVisibleDomains(ctx context.Context, actorID string) ([]domain.Domain, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return nil, err
	}
	predicate, args := resourceReadPredicate("domains.project_id", actorID, domain.PermissionCertificateRead)
	rows, err := r.db.QueryContext(ctx, Rebind(r.driver, visibleDomainSelect+` WHERE `+predicate+` ORDER BY domains.name, domains.id`), args...)
	if err != nil {
		return nil, fmt.Errorf("查询可见域名失败：%w", err)
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
		return nil, fmt.Errorf("读取可见域名失败：%w", err)
	}
	return items, nil
}

func (r *SQLProjectMemberRepository) GetVisibleDomain(ctx context.Context, actorID, id string) (domain.Domain, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return domain.Domain{}, err
	}
	predicate, args := resourceReadPredicate("domains.project_id", actorID, domain.PermissionCertificateRead)
	args = append(args, id)
	item, err := scanDomain(r.db.QueryRowContext(ctx, Rebind(r.driver,
		visibleDomainSelect+` WHERE `+predicate+` AND domains.id = ?`), args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Domain{}, ErrProjectAccessDenied
	}
	return item, err
}

func (r *SQLProjectMemberRepository) ListVisibleServers(ctx context.Context, actorID string) ([]deployment.Server, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return nil, err
	}
	predicate, args := resourceReadPredicate("servers.project_id", actorID, domain.PermissionServerRead)
	return r.readVisibleServers(ctx, serverSelectQualified+` WHERE `+predicate+` ORDER BY servers.name, servers.id`, args)
}

func (r *SQLProjectMemberRepository) GetVisibleServer(ctx context.Context, actorID, id string) (deployment.Server, error) {
	if err := r.resourceReaderReady(actorID); err != nil {
		return deployment.Server{}, err
	}
	predicate, args := resourceReadPredicate("servers.project_id", actorID, domain.PermissionServerRead)
	args = append(args, id)
	item, err := scanServer(r.db.QueryRowContext(ctx, Rebind(r.driver,
		serverSelectQualified+` WHERE `+predicate+` AND servers.id = ?`), args...))
	if errors.Is(err, sql.ErrNoRows) {
		return deployment.Server{}, ErrProjectAccessDenied
	}
	return item, err
}

func (r *SQLProjectMemberRepository) ListVisibleDomainServers(ctx context.Context, actorID, domainID string) ([]deployment.Server, error) {
	// 对未知和无权限域名统一拒绝；随后的查询仍重判双方权限，撤权后不会返回旧结果。
	if _, err := r.GetVisibleDomain(ctx, actorID, domainID); err != nil {
		return nil, err
	}
	domainPredicate, args := resourceReadPredicate("domains.project_id", actorID, domain.PermissionCertificateRead)
	serverPredicate, serverArgs := resourceReadPredicate("servers.project_id", actorID, domain.PermissionServerRead)
	args = append(args, serverArgs...)
	args = append(args, domainID)
	return r.readVisibleServers(ctx, serverSelectQualified+`
		JOIN domain_servers visible_binding ON visible_binding.server_id = servers.id
		JOIN domains ON domains.id = visible_binding.domain_id AND domains.project_id = servers.project_id
		WHERE `+domainPredicate+` AND `+serverPredicate+` AND domains.id = ? ORDER BY servers.name, servers.id`, args)
}

func (r *SQLProjectMemberRepository) ListVisibleServerDomains(ctx context.Context, actorID, serverID string) ([]domain.Domain, error) {
	if _, err := r.GetVisibleServer(ctx, actorID, serverID); err != nil {
		return nil, err
	}
	domainPredicate, args := resourceReadPredicate("domains.project_id", actorID, domain.PermissionCertificateRead)
	serverPredicate, serverArgs := resourceReadPredicate("servers.project_id", actorID, domain.PermissionServerRead)
	args = append(args, serverArgs...)
	args = append(args, serverID)
	rows, err := r.db.QueryContext(ctx, Rebind(r.driver, visibleDomainSelect+`
		JOIN domain_servers visible_binding ON visible_binding.domain_id = domains.id
		JOIN servers ON servers.id = visible_binding.server_id AND servers.project_id = domains.project_id
		WHERE `+domainPredicate+` AND `+serverPredicate+` AND servers.id = ? ORDER BY domains.name, domains.id`), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query visible server domains: %w", err)
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
		return nil, fmt.Errorf("failed to read visible server domains: %w", err)
	}
	return items, nil
}

func (r *SQLProjectMemberRepository) readVisibleServers(ctx context.Context, query string, args []any) ([]deployment.Server, error) {
	rows, err := r.db.QueryContext(ctx, Rebind(r.driver, query), args...)
	if err != nil {
		return nil, fmt.Errorf("查询可见服务器失败：%w", err)
	}
	defer rows.Close()
	items := make([]deployment.Server, 0)
	for rows.Next() {
		item, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取可见服务器失败：%w", err)
	}
	return items, nil
}
