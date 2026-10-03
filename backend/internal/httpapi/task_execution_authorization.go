package httpapi

import (
	"context"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

// 把操作者与项目绑定到实际签发快照，防止授权后修改验证方式绕过 DNS 权限。
func (s *Server) withCertificateIssueAuthorization(ctx context.Context, actor, domainID, projectID string, retry bool) context.Context {
	return certificate.WithIssueAuthorizer(ctx, func(ctx context.Context, item domain.Domain) error {
		authorizer, ok := s.projectMembers.(repository.ProjectAuthorizer)
		if !ok || actor == "" || projectID == "" || item.ID != domainID || item.ResourceScope.Normalize().ProjectID != projectID {
			return repository.ErrProjectAccessDenied
		}
		kind, err := domain.NormalizeChallengeType(item.ChallengeType, append([]string{item.Name}, item.SANs...))
		if err != nil {
			return err
		}
		permissions := []domain.ProjectPermission{domain.PermissionCertificateManage, domain.PermissionDeploymentExecute}
		if kind == domain.ChallengeDNS01 {
			permissions = append(permissions, domain.PermissionDNSCredentialUse)
		}
		if retry {
			permissions = append(permissions, domain.PermissionTaskRetry)
		}
		for _, permission := range permissions {
			if err := authorizer.AuthorizeProject(ctx, actor, projectID, permission); err != nil {
				return err
			}
		}
		return nil
	})
}

// 排队期间权限和资源归属可能变化；历史快照只用于审计，不作为授权凭证。
// 只检查本次即将执行的操作，后台自动续期不从此 HTTP 任务入口执行。
func (s *Server) authorizeCertificateTaskExecution(ctx context.Context, task repository.Task, attempt repository.TaskAttempt) error {
	authorizer, ok := s.projectMembers.(repository.ProjectAuthorizer)
	if !ok || s.projectResources == nil || task.OperatorID == "" || task.ScopeType != repository.ScopeProject || task.ProjectID == "" {
		return repository.ErrProjectAccessDenied
	}
	resource, err := s.projectResources.GetVisibleDomain(ctx, task.OperatorID, task.DomainID)
	if err != nil {
		return err
	}
	if resource.ID != task.DomainID || resource.ResourceScope.Normalize().ProjectID != task.ProjectID {
		return repository.ErrProjectAccessDenied
	}
	permissions := []domain.ProjectPermission{domain.PermissionDeploymentExecute}
	switch task.TaskType {
	case "certificate_issue":
		permissions = append(permissions, domain.PermissionCertificateManage)
		kind, err := domain.NormalizeChallengeType(resource.ChallengeType, append([]string{resource.Name}, resource.SANs...))
		if err != nil {
			return err
		}
		if kind == domain.ChallengeDNS01 {
			permissions = append(permissions, domain.PermissionDNSCredentialUse)
		}
	case "certificate_deploy":
	case "certificate_restore":
		permissions = append(permissions, domain.PermissionPrivateKeyRead)
	case "certificate_import":
		permissions = append(permissions, domain.PermissionCertificateManage)
	case "certificate_validate":
		permissions = []domain.ProjectPermission{domain.PermissionPrivateKeyRead}
	default:
		return repository.ErrProjectAccessDenied
	}
	if attempt.AttemptNumber > 1 {
		// 重试仓储已将操作者更新为本次重试者，不能沿用首次提交者权限。
		permissions = append(permissions, domain.PermissionTaskRetry, domain.PermissionCertificateManage)
	}
	for _, permission := range permissions {
		if err := authorizer.AuthorizeProject(ctx, task.OperatorID, task.ProjectID, permission); err != nil {
			return err
		}
	}
	return nil
}
