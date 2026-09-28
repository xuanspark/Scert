package domain

// ProjectRole 是项目内的能力集合，与登录角色和用户分组相互独立。
type ProjectRole string

const (
	ProjectRoleAdmin            ProjectRole = "project_admin"
	ProjectRoleCertificateAdmin ProjectRole = "certificate_admin"
	ProjectRoleDeploymentAdmin  ProjectRole = "deployment_admin"
	ProjectRoleDNSAdmin         ProjectRole = "dns_admin"
	ProjectRoleAuditViewer      ProjectRole = "audit_viewer"
	ProjectRoleReadOnly         ProjectRole = "read_only"
)

type ProjectPermission string

const (
	PermissionProjectRead        ProjectPermission = "project.read"
	PermissionProjectManage      ProjectPermission = "project.manage"
	PermissionMembersManage      ProjectPermission = "members.manage"
	PermissionCertificateRead    ProjectPermission = "certificate.read"
	PermissionCertificateManage  ProjectPermission = "certificate.manage"
	PermissionPrivateKeyRead     ProjectPermission = "certificate.private_key.read"
	PermissionServerRead         ProjectPermission = "server.read"
	PermissionServerManage       ProjectPermission = "server.manage"
	PermissionDeploymentRead     ProjectPermission = "deployment.read"
	PermissionDeploymentExecute  ProjectPermission = "deployment.execute"
	PermissionDeploymentRollback ProjectPermission = "deployment.rollback"
	PermissionDNSRead            ProjectPermission = "dns.read"
	PermissionDNSManage          ProjectPermission = "dns.manage"
	PermissionDNSCredentialUse   ProjectPermission = "dns.credential.use"
	PermissionAuditRead          ProjectPermission = "audit.read"
	PermissionTaskRead           ProjectPermission = "task.read"
	PermissionTaskCancel         ProjectPermission = "task.cancel"
)

func (r ProjectRole) Valid() bool {
	switch r {
	case ProjectRoleAdmin, ProjectRoleCertificateAdmin, ProjectRoleDeploymentAdmin,
		ProjectRoleDNSAdmin, ProjectRoleAuditViewer, ProjectRoleReadOnly:
		return true
	}
	return false
}

func (p ProjectPermission) Valid() bool {
	switch p {
	case PermissionProjectRead, PermissionProjectManage, PermissionMembersManage,
		PermissionCertificateRead, PermissionCertificateManage, PermissionPrivateKeyRead,
		PermissionServerRead, PermissionServerManage, PermissionDeploymentRead,
		PermissionDeploymentExecute, PermissionDeploymentRollback, PermissionDNSRead,
		PermissionDNSManage, PermissionDNSCredentialUse, PermissionAuditRead,
		PermissionTaskRead, PermissionTaskCancel:
		return true
	}
	return false
}

// Allows 只计算项目角色能力；调用方还必须核对实时账户状态和资源归属。
func (r ProjectRole) Allows(p ProjectPermission) bool {
	if !r.Valid() || !p.Valid() {
		return false
	}
	if r == ProjectRoleAdmin || p == PermissionProjectRead {
		return true
	}
	switch r {
	case ProjectRoleCertificateAdmin:
		return p == PermissionCertificateRead || p == PermissionCertificateManage || p == PermissionPrivateKeyRead
	case ProjectRoleDeploymentAdmin:
		return p == PermissionCertificateRead || p == PermissionServerRead || p == PermissionServerManage ||
			p == PermissionDeploymentRead || p == PermissionDeploymentExecute || p == PermissionDeploymentRollback
	case ProjectRoleDNSAdmin:
		return p == PermissionDNSRead || p == PermissionDNSManage || p == PermissionDNSCredentialUse
	case ProjectRoleAuditViewer:
		return p == PermissionAuditRead || p == PermissionTaskRead
	case ProjectRoleReadOnly:
		return p == PermissionCertificateRead || p == PermissionServerRead ||
			p == PermissionDeploymentRead || p == PermissionDNSRead || p == PermissionTaskRead
	}
	return false
}
